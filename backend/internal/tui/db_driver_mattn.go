//go:build !sqlite_modernc

package tui

// sqliteDriverName mirrors internal/database: the release build links the
// pure-Go driver under the sqlite_modernc tag and the cgo one otherwise. The
// console must open the panel database with whatever driver the binary was
// linked against, so it follows the same tag.
const sqliteDriverName = "sqlite3"
