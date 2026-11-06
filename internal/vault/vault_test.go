package vault

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tobischo/gokeepasslib/v3"
)

const (
	fixturePassword        = "test-password"
	libraryFixturePassword = "abcdefg12345678"
)

// copyFixture gives each test its own writable copy of a testdata file.
func copyFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func find(entries []Entry, title string) *Entry {
	for i := range entries {
		if entries[i].Title == title {
			return &entries[i]
		}
	}
	return nil
}

func TestOpenReadsEntriesAndSkipsRecycleBin(t *testing.T) {
	v, err := Open(copyFixture(t, "kdbx4.kdbx"), fixturePassword)
	if err != nil {
		t.Fatal(err)
	}
	entries := v.Entries()
	if len(entries) != 2 || entries[0].Title != "Bank" || entries[1].Title != "Mail" {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[1].Username != "alice" || entries[1].Password != "s3cret-new" {
		t.Fatalf("Mail = %+v", entries[1])
	}
}

func TestOpenWrongPassword(t *testing.T) {
	for _, name := range []string{"kdbx4.kdbx", "kdbx3.kdbx"} {
		if _, err := Open(copyFixture(t, name), "nope"); !errors.Is(err, ErrWrongPassword) {
			t.Errorf("%s: err = %v, want ErrWrongPassword", name, err)
		}
	}
}

func TestAddKeepsExistingData(t *testing.T) {
	path := copyFixture(t, "kdbx4.kdbx")
	v, err := Open(path, fixturePassword)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Add("Nowy", "jan", "pąssword"); err != nil {
		t.Fatal(err)
	}
	if e := find(v.Entries(), "Nowy"); e == nil || e.Username != "jan" || e.Password != "pąssword" {
		t.Fatalf("new entry = %+v", e)
	}

	// A fresh open must see the same thing, not just this Vault's cache.
	v2, err := Open(path, fixturePassword)
	if err != nil {
		t.Fatal(err)
	}
	if len(v2.Entries()) != 3 {
		t.Fatalf("entries = %+v", v2.Entries())
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	db, err := decode(f, gokeepasslib.NewPasswordCredentials(fixturePassword))
	if err != nil {
		t.Fatal(err)
	}
	var mail *gokeepasslib.Entry
	walkGroups(db.Content.Root.Groups, func(*gokeepasslib.Group) bool { return false },
		func(e *gokeepasslib.Entry) {
			if e.GetTitle() == "Mail" {
				mail = e
			}
		})
	if mail == nil {
		t.Fatal("Mail entry lost")
	}
	if got := mail.GetContent("Custom"); got != "custom-value" {
		t.Errorf("custom field = %q", got)
	}
	if got := mail.GetContent("URL"); got != "https://mail.example" {
		t.Errorf("url = %q", got)
	}
	if got := len(mail.Histories); got != 1 || len(mail.Histories[0].Entries) != 1 {
		t.Errorf("history lost: %+v", mail.Histories)
	}
	if len(mail.Binaries) != 1 || mail.Binaries[0].Name != "file.txt" {
		t.Errorf("attachment ref lost: %+v", mail.Binaries)
	}
	if got := countEntries(db); got != 4 { // Bank, Mail, Trashed, Nowy
		t.Errorf("total entries = %d", got)
	}

	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Errorf("backup missing: %v", err)
	}
}

func TestAddSeesExternalChanges(t *testing.T) {
	path := copyFixture(t, "kdbx4.kdbx")
	stale, err := Open(path, fixturePassword)
	if err != nil {
		t.Fatal(err)
	}
	other, err := Open(path, fixturePassword)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Add("FromKeePass", "u", "p"); err != nil {
		t.Fatal(err)
	}
	if !stale.Changed() {
		t.Error("Changed() = false after the file was rewritten")
	}
	if err := stale.Add("FromVault", "u", "p"); err != nil {
		t.Fatal(err)
	}
	entries := stale.Entries()
	if find(entries, "FromKeePass") == nil || find(entries, "FromVault") == nil {
		t.Fatalf("entries = %+v", entries)
	}
}

// Reusing the key and nonce across versions of the file would let anyone with
// two versions XOR them, so no header random may survive a save.
func TestAddRenewsSeeds(t *testing.T) {
	path := copyFixture(t, "kdbx4.kdbx")
	header := func() *gokeepasslib.FileHeaders {
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		db, err := decode(f, gokeepasslib.NewPasswordCredentials(fixturePassword))
		if err != nil {
			t.Fatal(err)
		}
		fh := *db.Header.FileHeaders
		fh.MasterSeed = bytes.Clone(fh.MasterSeed)
		fh.EncryptionIV = bytes.Clone(fh.EncryptionIV)
		return &fh
	}
	before := header()
	v, err := Open(path, fixturePassword)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Add("x", "y", "z"); err != nil {
		t.Fatal(err)
	}
	after := header()
	if bytes.Equal(before.MasterSeed, after.MasterSeed) {
		t.Error("master seed reused")
	}
	if bytes.Equal(before.EncryptionIV, after.EncryptionIV) {
		t.Error("IV reused")
	}
	if before.KdfParameters.Salt == after.KdfParameters.Salt {
		t.Error("KDF salt reused")
	}
}

func TestCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nowa.kdbx")
	v, err := Create(path, "hasło")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Entries()) != 0 {
		t.Fatalf("entries = %+v", v.Entries())
	}
	if err := v.Add("a", "b", "c"); err != nil {
		t.Fatal(err)
	}
	v2, err := Open(path, "hasło")
	if err != nil {
		t.Fatal(err)
	}
	if len(v2.Entries()) != 1 {
		t.Fatalf("entries = %+v", v2.Entries())
	}
	if _, err := Create(path, "x"); err == nil {
		t.Fatal("Create replaced an existing file")
	}
}

func TestAddRequiresTitle(t *testing.T) {
	v, err := Open(copyFixture(t, "kdbx4.kdbx"), fixturePassword)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Add("", "u", "p"); err == nil {
		t.Fatal("expected error")
	}
}

// Runs where keepassxc-cli is installed (the CI test job).
func TestKeePassXCReadsOurFile(t *testing.T) {
	cli, err := exec.LookPath("keepassxc-cli")
	if err != nil {
		t.Skip("keepassxc-cli not installed")
	}
	path := filepath.Join(t.TempDir(), "x.kdbx")
	v, err := Create(path, fixturePassword)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Add("Nowy", "jan", "pass"); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(cli, "show", "-q", "-s", path, "Nowy")
	cmd.Stdin = strings.NewReader(fixturePassword + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("keepassxc-cli: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "jan") || !strings.Contains(string(out), "pass") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

// Fixtures copied from gokeepasslib's test suite (written by KeePass/KeePassXC).
func TestFormats(t *testing.T) {
	for name, password := range map[string]string{
		"kdbx3.kdbx":               libraryFixturePassword,
		"kdbx4-chacha-argon2.kdbx": libraryFixturePassword,
		"kdbx4-twofish.kdbx":       "test1234test",
		"kdbx4-nocompression.kdbx": libraryFixturePassword,
		"kdbx41.kdbx":              libraryFixturePassword,
	} {
		t.Run(name, func(t *testing.T) {
			path := copyFixture(t, name)
			v, err := Open(path, password)
			if err != nil {
				t.Fatal(err)
			}
			before := len(v.Entries())
			if err := v.Add("Nowy", "jan", "pass"); err != nil {
				t.Fatal(err)
			}
			v2, err := Open(path, password)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(v2.Entries()); got != before+1 {
				t.Fatalf("entries = %d, want %d", got, before+1)
			}
			if e := find(v2.Entries(), "Nowy"); e == nil || e.Password != "pass" {
				t.Fatalf("new entry = %+v", e)
			}
			if dir := os.Getenv("VAULT_KEEP_DIR"); dir != "" {
				data, _ := os.ReadFile(path)
				os.WriteFile(filepath.Join(dir, name), data, 0o600)
			}
		})
	}
}
