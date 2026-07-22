#ifndef PETITWEB_CGOSQLITE_BRIDGE_H
#define PETITWEB_CGOSQLITE_BRIDGE_H

#include <stdint.h>

typedef struct sqlite3 pw_sqlite3;
typedef struct sqlite3_stmt pw_sqlite3_stmt;

int pw_sqlite_open(const char *name, pw_sqlite3 **db);
int pw_sqlite_close(pw_sqlite3 *db);
const char *pw_sqlite_errmsg(pw_sqlite3 *db);
int pw_sqlite_extended_errcode(pw_sqlite3 *db);
void pw_sqlite_interrupt(pw_sqlite3 *db);
int pw_sqlite_busy_timeout(pw_sqlite3 *db, int milliseconds);
int pw_sqlite_prepare(pw_sqlite3 *db, const char *sql, int length,
                      pw_sqlite3_stmt **stmt);
int pw_sqlite_step(pw_sqlite3_stmt *stmt);
int pw_sqlite_reset(pw_sqlite3_stmt *stmt);
int pw_sqlite_clear_bindings(pw_sqlite3_stmt *stmt);
int pw_sqlite_finalize(pw_sqlite3_stmt *stmt);
int pw_sqlite_bind_parameter_count(pw_sqlite3_stmt *stmt);
int pw_sqlite_bind_parameter_index(pw_sqlite3_stmt *stmt, const char *name);
int pw_sqlite_bind_null(pw_sqlite3_stmt *stmt, int index);
int pw_sqlite_bind_int64(pw_sqlite3_stmt *stmt, int index, int64_t value);
int pw_sqlite_bind_double(pw_sqlite3_stmt *stmt, int index, double value);
int pw_sqlite_bind_text(pw_sqlite3_stmt *stmt, int index, const char *value,
                        int length);
int pw_sqlite_bind_blob(pw_sqlite3_stmt *stmt, int index, const void *value,
                        int length);
int pw_sqlite_column_count(pw_sqlite3_stmt *stmt);
const char *pw_sqlite_column_name(pw_sqlite3_stmt *stmt, int column);
const char *pw_sqlite_column_decltype(pw_sqlite3_stmt *stmt, int column);
int pw_sqlite_column_type(pw_sqlite3_stmt *stmt, int column);
int64_t pw_sqlite_column_int64(pw_sqlite3_stmt *stmt, int column);
double pw_sqlite_column_double(pw_sqlite3_stmt *stmt, int column);
const unsigned char *pw_sqlite_column_text(pw_sqlite3_stmt *stmt, int column);
const void *pw_sqlite_column_blob(pw_sqlite3_stmt *stmt, int column);
int pw_sqlite_column_bytes(pw_sqlite3_stmt *stmt, int column);
int64_t pw_sqlite_changes(pw_sqlite3 *db);
int64_t pw_sqlite_last_insert_rowid(pw_sqlite3 *db);
const char *pw_sqlite_libversion(void);

#define PW_SQLITE_OK 0
#define PW_SQLITE_ERROR 1
#define PW_SQLITE_BUSY 5
#define PW_SQLITE_INTERRUPT 9
#define PW_SQLITE_ROW 100
#define PW_SQLITE_DONE 101
#define PW_SQLITE_INTEGER 1
#define PW_SQLITE_FLOAT 2
#define PW_SQLITE_TEXT 3
#define PW_SQLITE_BLOB 4
#define PW_SQLITE_NULL 5

#endif
