//go:build tinygo && linux

// TinyGo's Linux musl archive intentionally omits a few POSIX wrappers that
// SQLite's unix VFS references. Keep these wrappers local to TinyGo builds.
#define _GNU_SOURCE
#include <fcntl.h>
#include <sys/stat.h>
#include <sys/syscall.h>
#include <sys/time.h>
#include <time.h>
#include <unistd.h>

int stat(const char *path, struct stat *result) {
  return (int)syscall(SYS_newfstatat, AT_FDCWD, path, result, 0);
}

int lstat(const char *path, struct stat *result) {
  return (int)syscall(SYS_newfstatat, AT_FDCWD, path, result,
                      AT_SYMLINK_NOFOLLOW);
}

int fstat(int fd, struct stat *result) {
  return (int)syscall(SYS_fstat, fd, result);
}

int fchmod(int fd, mode_t mode) {
  return (int)syscall(SYS_fchmod, fd, mode);
}

int mkdir(const char *path, mode_t mode) {
  return (int)syscall(SYS_mkdirat, AT_FDCWD, path, mode);
}

int __futimesat(int fd, const char *path, const struct timeval times[2]) {
  if (times == 0) return (int)syscall(SYS_utimensat, fd, path, 0, 0);
  struct timespec spec[2];
  spec[0].tv_sec = times[0].tv_sec;
  spec[0].tv_nsec = times[0].tv_usec * 1000;
  spec[1].tv_sec = times[1].tv_sec;
  spec[1].tv_nsec = times[1].tv_usec * 1000;
  return (int)syscall(SYS_utimensat, fd, path, spec, 0);
}

void __procfdname(char *buffer, unsigned fd) {
  static const char prefix[] = "/proc/self/fd/";
  unsigned i = 0;
  while ((buffer[i] = prefix[i]) != 0) i++;
  if (fd == 0) {
    buffer[i++] = '0';
    buffer[i] = 0;
    return;
  }
  unsigned end = i;
  for (unsigned value = fd; value != 0; value /= 10) end++;
  buffer[end] = 0;
  while (fd != 0) {
    buffer[--end] = (char)('0' + fd % 10);
    fd /= 10;
  }
}
