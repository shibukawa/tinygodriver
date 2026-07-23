#ifndef PETITWEB_CGOSQLITE_SYS_IOCTL_H
#define PETITWEB_CGOSQLITE_SYS_IOCTL_H

/*
 * Prefer the target SDK header whenever it exists. TinyGo's macOS minimal SDK
 * omits it, while SQLite includes it unconditionally even though ioctl is only
 * used by Linux F2FS support in this build.
 */
#if defined(__has_include_next)
#if __has_include_next(<sys/ioctl.h>)
#include_next <sys/ioctl.h>
#else
int ioctl(int descriptor, unsigned long request, ...);
#endif
#else
#include_next <sys/ioctl.h>
#endif

#endif
