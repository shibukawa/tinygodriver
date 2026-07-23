#ifndef PETITWEB_CGOSQLITE_SYS_MOUNT_H
#define PETITWEB_CGOSQLITE_SYS_MOUNT_H

/* Use the complete target SDK definition whenever available. */
#if defined(__has_include_next)
#if __has_include_next(<sys/mount.h>)
#include_next <sys/mount.h>
#else

#include <string.h>

/*
 * TinyGo's macOS minimal SDK omits filesystem metadata declarations. SQLite
 * only reads f_flags and f_fstypename in the non-locking-style build. Treat
 * the already-opened filesystem as a normal local read/write filesystem;
 * open(), fcntl(), and fsync() still provide the actual I/O and locking.
 */
#define MNT_RDONLY 0x00000001
#define MNT_LOCAL 0x00001000

struct statfs {
  unsigned long f_flags;
  char f_fstypename[16];
};

static inline int fstatfs(int descriptor, struct statfs *result) {
  (void)descriptor;
  result->f_flags = MNT_LOCAL;
  memset(result->f_fstypename, 0, sizeof(result->f_fstypename));
  return 0;
}

static inline int statfs(const char *path, struct statfs *result) {
  (void)path;
  return fstatfs(-1, result);
}

#endif
#else
#include_next <sys/mount.h>
#endif

#endif
