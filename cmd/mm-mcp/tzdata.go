package main

// The timezone database travels inside the binary: a Windows machine, or a
// container built from scratch, has none to read, and set_post_reminder reads
// a time in the person's own zone.
import _ "time/tzdata"
