"""Generates KDBX fixtures with pykeepass, an implementation independent of the
library under test. Usage: pip install pykeepass && python make_fixtures.py"""
import pathlib
import pykeepass

here = pathlib.Path(__file__).parent
pw = "test-password"

def fill(kp):
    work = kp.add_group(kp.root_group, "Work")
    e = kp.add_entry(work, "Mail", "alice", "s3cret-ążć", url="https://mail.example", notes="note")
    e.set_custom_property("Custom", "custom-value", protect=True)
    aid = kp.add_binary(b"attachment-bytes")
    e.add_attachment(aid, "file.txt")
    e.save_history()
    e.password = "s3cret-new"
    kp.add_entry(kp.root_group, "Bank", "bob", "b4nk")
    trash = kp.add_entry(kp.root_group, "Trashed", "x", "y")
    kp.trash_entry(trash)

kp = pykeepass.create_database(str(here / "kdbx4.kdbx"), password=pw)
fill(kp)
kp.save()
