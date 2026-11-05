package plugin

import "musicgo/internal/plugin/polyfill"

// Storage is the per-plugin persistent key-value store the host injects,
// backed by SQLite and scoped by plugin platform.
type Storage = polyfill.Storage
