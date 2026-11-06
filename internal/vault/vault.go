// Package vault reads KeePass (KDBX 3.1/4.x) databases and appends entries to
// them. It has no Windows dependencies, so it can be tested on any platform.
package vault

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tobischo/gokeepasslib/v3"
	"github.com/tobischo/gokeepasslib/v3/wrappers"
)

// ErrWrongPassword means the password does not open the database.
var ErrWrongPassword = errors.New("wrong password")

// Entry is the part of a KeePass entry this application works with.
type Entry struct {
	Title    string
	Username string
	Password string
}

// Vault is an unlocked database. Every change is made against the current file
// on disk, so edits made meanwhile in KeePass are never overwritten.
type Vault struct {
	path  string
	creds *gokeepasslib.DBCredentials

	mu      sync.Mutex
	entries []Entry
	modTime time.Time
}

// Open unlocks the database at path.
func Open(path, password string) (*Vault, error) {
	v := &Vault{path: path, creds: gokeepasslib.NewPasswordCredentials(password)}
	if err := v.Reload(); err != nil {
		return nil, err
	}
	return v, nil
}

// Create writes a new, empty KDBX 4 database. It refuses to replace a file.
func Create(path, password string) (*Vault, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("%s already exists", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	db := gokeepasslib.NewDatabase(gokeepasslib.WithDatabaseKDBXVersion41())
	db.Credentials = gokeepasslib.NewPasswordCredentials(password)
	db.Content.Meta.DatabaseName = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	root := &db.Content.Root.Groups[0]
	root.Name = db.Content.Meta.DatabaseName
	root.Entries = nil // drop the library's sample entry

	v := &Vault{path: path, creds: db.Credentials}
	if err := v.write(db, 0); err != nil {
		return nil, err
	}
	if err := v.Reload(); err != nil {
		return nil, err
	}
	return v, nil
}

// Entries returns a snapshot of the entries, sorted by title.
func (v *Vault) Entries() []Entry {
	v.mu.Lock()
	defer v.mu.Unlock()
	return slices.Clone(v.entries)
}

// Path returns the location of the database file.
func (v *Vault) Path() string { return v.path }

// Reload rereads the database from disk.
func (v *Vault) Reload() error {
	db, modTime, err := v.read()
	if err != nil {
		return err
	}
	v.setState(db, modTime)
	return nil
}

// Changed reports whether the file differs from the state last loaded.
func (v *Vault) Changed() bool {
	info, err := os.Stat(v.path)
	if err != nil {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return !info.ModTime().Equal(v.modTime)
}

// Add stores a new entry in the database's root group.
func (v *Vault) Add(title, username, password string) error {
	if title == "" {
		return errors.New("title is required")
	}

	db, _, err := v.read()
	if err != nil {
		return err
	}
	if len(db.Content.Root.Groups) == 0 {
		return errors.New("database has no root group")
	}

	e := gokeepasslib.NewEntry()
	e.Values = []gokeepasslib.ValueData{
		{Key: "Title", Value: gokeepasslib.V{Content: title}},
		{Key: "UserName", Value: gokeepasslib.V{Content: username}},
		{Key: "Password", Value: gokeepasslib.V{Content: password, Protected: wrappers.NewBoolWrapper(true)}},
		{Key: "URL", Value: gokeepasslib.V{}},
		{Key: "Notes", Value: gokeepasslib.V{}},
	}
	root := &db.Content.Root.Groups[0]
	root.Entries = append(root.Entries, e)

	if err := v.write(db, countEntries(db)); err != nil {
		return err
	}
	return v.Reload()
}

// read decodes the file and leaves protected values unlocked.
func (v *Vault) read() (*gokeepasslib.Database, time.Time, error) {
	// Stat first: a write landing in between is then seen by Changed. The file
	// is not held open during key derivation, as that would block replacing it
	// on Windows.
	info, err := os.Stat(v.path)
	if err != nil {
		return nil, time.Time{}, err
	}
	data, err := os.ReadFile(v.path)
	if err != nil {
		return nil, time.Time{}, err
	}
	db, err := decode(bytes.NewReader(data), v.creds)
	if err != nil {
		return nil, time.Time{}, err
	}
	return db, info.ModTime(), nil
}

func decode(r io.Reader, creds *gokeepasslib.DBCredentials) (*gokeepasslib.Database, error) {
	db := gokeepasslib.NewDatabase()
	db.Credentials = creds
	if err := gokeepasslib.NewDecoder(r).Decode(db); err != nil {
		// The library's password errors are unexported; both start this way.
		if errors.Is(err, gokeepasslib.ErrInvalidDatabaseOrCredentials) ||
			strings.HasPrefix(err.Error(), "Wrong password?") {
			return nil, ErrWrongPassword
		}
		return nil, fmt.Errorf("read database: %w", err)
	}
	if err := db.UnlockProtectedEntries(); err != nil {
		return nil, fmt.Errorf("unlock database: %w", err)
	}
	return db, nil
}

// write replaces the database file. db must be unlocked. The new file is only
// put in place after it has been decoded again with wantEntries entries; the
// previous version is kept next to it as <name>.bak.
func (v *Vault) write(db *gokeepasslib.Database, wantEntries int) error {
	if err := renewSeeds(db); err != nil {
		return err
	}
	if err := db.LockProtectedEntries(); err != nil {
		return fmt.Errorf("lock database: %w", err)
	}

	var buf bytes.Buffer
	if err := gokeepasslib.NewEncoder(&buf).Encode(db); err != nil {
		return fmt.Errorf("encode database: %w", err)
	}
	check, err := decode(bytes.NewReader(buf.Bytes()), v.creds)
	if err != nil {
		return fmt.Errorf("verify database: %w", err)
	}
	if got := countEntries(check); got != wantEntries {
		return fmt.Errorf("verify database: %d entries, want %d", got, wantEntries)
	}

	tmp, err := os.CreateTemp(filepath.Dir(v.path), filepath.Base(v.path)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if old, err := os.ReadFile(v.path); err == nil {
		if err := os.WriteFile(v.path+".bak", old, 0o600); err != nil {
			return fmt.Errorf("write backup: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return renameRetry(tmp.Name(), v.path)
}

// renameRetry tolerates antivirus and sync clients, which briefly hold files
// open on Windows.
func renameRetry(from, to string) error {
	var err error
	for range 10 {
		if err = os.Rename(from, to); err == nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return err
}

// renewSeeds replaces all per-save random values, as KeePass does on each save.
// gokeepasslib keeps the values it read, which would encrypt every new version
// of the file with the same key and nonce. Without this, anyone holding two
// versions (sync history, backups) could XOR them to recover the difference
// between their plaintexts.
func renewSeeds(db *gokeepasslib.Database) error {
	h := db.Header.FileHeaders
	fields := [][]byte{h.MasterSeed, h.EncryptionIV, h.TransformSeed, h.ProtectedStreamKey, h.StreamStartBytes}
	if h.KdfParameters != nil {
		fields = append(fields, h.KdfParameters.Salt[:])
	}
	if ih := db.Content.InnerHeader; ih != nil {
		fields = append(fields, ih.InnerRandomStreamKey)
	}
	for _, field := range fields {
		if _, err := rand.Read(field); err != nil {
			return fmt.Errorf("generate seeds: %w", err)
		}
	}
	return nil
}

func (v *Vault) setState(db *gokeepasslib.Database, modTime time.Time) {
	var entries []Entry
	recycleBin := db.Content.Meta.RecycleBinUUID
	skip := func(g *gokeepasslib.Group) bool {
		return db.Content.Meta.RecycleBinEnabled.Bool && g.UUID == recycleBin
	}
	walkGroups(db.Content.Root.Groups, skip, func(e *gokeepasslib.Entry) {
		entries = append(entries, Entry{
			Title:    e.GetTitle(),
			Username: e.GetContent("UserName"),
			Password: e.GetPassword(),
		})
	})
	slices.SortStableFunc(entries, func(a, b Entry) int {
		return strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title))
	})

	v.mu.Lock()
	defer v.mu.Unlock()
	v.entries = entries
	v.modTime = modTime
}

func countEntries(db *gokeepasslib.Database) int {
	n := 0
	walkGroups(db.Content.Root.Groups, func(*gokeepasslib.Group) bool { return false },
		func(*gokeepasslib.Entry) { n++ })
	return n
}

func walkGroups(groups []gokeepasslib.Group, skip func(*gokeepasslib.Group) bool, visit func(*gokeepasslib.Entry)) {
	for i := range groups {
		g := &groups[i]
		if skip(g) {
			continue
		}
		for j := range g.Entries {
			visit(&g.Entries[j])
		}
		walkGroups(g.Groups, skip, visit)
	}
}
