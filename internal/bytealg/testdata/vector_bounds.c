/* Exact-size allocations make even read-only vector overruns visible to ASan. */
#include <assert.h>
#include <stdlib.h>
#include "../vector_tinygo.c"

int main(void) {
    for (size_t len = 0; len <= 512; len++) {
        for (size_t align = 0; align < 32; align++) {
            uint8_t *allocation = malloc(len+align+1);
            assert(allocation);
            uint8_t *p = allocation+align+1;
            for (size_t i = 0; i < len; i++) p[i] = (uint8_t)(i*37+len);
            int valid = 1;
            for (size_t i = 0; i < len; i++)
                if ((p[i] < 32 && p[i] != 9) || p[i] == 127) valid = 0;
            assert(tinygodriver_header_value(p,len) == valid);
            for (unsigned c = 0; c < 256; c++) {
                ptrdiff_t want = -1;
                for (size_t i = 0; i < len; i++) if (p[i] == c) { want = i; break; }
                assert(tinygodriver_index_byte(p,len,c) == want);
            }
            size_t processed = tinygodriver_mask(p,len,0x7391fa02);
            assert(processed == (len & ~(size_t)15));
            for (size_t i = 0; i < len; i++) {
                uint8_t want = (uint8_t)(i*37+len);
                if (i < processed) want ^= (uint8_t)(0x7391fa02u >> (8*(i&3)));
                assert(p[i] == want);
            }
            free(allocation);
        }
    }
    return 0;
}
