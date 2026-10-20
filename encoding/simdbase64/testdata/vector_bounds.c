#include <assert.h>
#include <stdlib.h>
#include <string.h>
#include "../vector_tinygo.c"

/* Standalone object-boundary checks: build with Clang ASan/UBSan. */
int main(void) {
    if (!tinygodriver_base64_available()) return 0;
    const size_t sizes[] = {0,1,15,16,17,47,48,49,51,52,63,64,65,76,77,78,79,80,81,95,96,97,127,128,129,191,192,193,255,256,257,319,320,321,511,512,513,1024};
    for (int url=0; url<2; url++) for (size_t off=0; off<16; off++) {
        for (size_t si=0; si<sizeof(sizes)/sizeof(sizes[0]); si++) {
            size_t slen=sizes[si];
            uint8_t *sb=malloc(off+slen+!slen), *src=sb+off;
            for (size_t di=0; di<sizeof(sizes)/sizeof(sizes[0]); di++) {
                size_t dlen=sizes[di];
                uint8_t *db=malloc(off+dlen+!dlen), *dst=db+off;
                memset(src, 0xff, slen);
                size_t n=tinygodriver_base64_encode(dst,dlen,src,slen,url);
                assert(n<=slen && n%12==0 && n/3*4<=dlen);
                for (size_t i=0; i<n/3*4; i++) assert(dst[i]==(url?'_':'/'));
                for (int mime=0; mime<2; mime++) {
                    memset(src,'A',slen);
                    if (mime) for (size_t i=76; i<slen; i+=77) src[i]='\n';
                    tinygodriver_base64_result r=tinygodriver_base64_decode(dst,dlen,src,slen,url);
                    assert(r.read<=slen && r.written<=dlen);
                    for (size_t i=0; i<r.written; i++) assert(dst[i]==0);
                }
                free(db);
            }
            memset(src,'A',slen);
            tinygodriver_base64_result r=tinygodriver_base64_decode(src,slen,src,slen,url);
            assert(r.read<=slen && r.written<=slen);
            for (size_t i=0; i<r.written; i++) assert(src[i]==0);
            free(sb);
        }
    }
    return 0;
}
