// Package sqlitevec compiles sqlite-vec 0.1.9 (the amalgamation in this folder,
// sqlite-vec-0.1.9-amalgamation.tar.gz from the GitHub release, SHA-256
// 3acd67cb4aff080c7050926fd3cf8227905fe5b7ee3829d8ee5024ab1283cf61) into the
// binary and registers it as an auto extension, so every SQLite connection the
// process opens has vec0 and vec_version(). The vec0 shadow tables in the
// production database were written by this version.
//
// sqlite3.h is github.com/mattn/go-sqlite3's sqlite3-binding.h, the header of
// the SQLite that driver compiles; sqlite-vec calls that SQLite directly
// (SQLITE_CORE).
//
// The npm build the TS app loads uses NEON on macOS arm64 and no SIMD on Linux,
// so this does the same. The cosine distance the indexes use has no SIMD path.
package sqlitevec

/*
#cgo CFLAGS: -DSQLITE_CORE -DSQLITE_VEC_STATIC -O2
#cgo darwin,arm64 CFLAGS: -DSQLITE_VEC_ENABLE_NEON
#cgo linux LDFLAGS: -lm
#include "sqlite3.h"
#include "sqlite-vec.h"

static int recall_register_vec(void) {
  return sqlite3_auto_extension((void (*)(void))sqlite3_vec_init);
}
*/
import "C"

import (
	"fmt"

	_ "github.com/mattn/go-sqlite3" // the SQLite this extension links against
)

func init() {
	if rc := C.recall_register_vec(); rc != 0 {
		panic(fmt.Sprintf("sqlite-vec: sqlite3_auto_extension returned %d", rc))
	}
}

// Version is the sqlite-vec version compiled in.
const Version = "v0.1.9"
