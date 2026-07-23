# tinygosqlite

`tinygosqlite` is Petitweb's small native SQLite driver for TinyGo and
`force_tinygo_logic` builds. It implements `database/sql` over a statically
linked copy of the official SQLite amalgamation; it does not use WebAssembly or
load the operating system's SQLite library.

The bundled SQLite is 3.53.3 (`sqlite-amalgamation-3530300.zip`). Loadable
extensions and double-quoted string literals are disabled. URI filenames accept
only `mode`, `cache`, `immutable`, and `busy_timeout` (0–60000 ms).

Import the higher-level `database/sql/sqlite` package unless a direct test
of this backend is required.

TinyGo 0.41.1 is tested natively on Linux and macOS arm64. The macOS minimal
SDK build uses standard malloc bookkeeping and POSIX locking because the SDK
omits the Mach zone-allocator and filesystem-specific locking headers used by
full Darwin SDKs.
