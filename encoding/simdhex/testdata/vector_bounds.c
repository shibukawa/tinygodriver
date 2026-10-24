#include <assert.h>
#include <stdlib.h>
#include "../vector_tinygo.c"

int main(void) {
    const char *alphabet="0123456789abcdef";
    for (size_t n=0;n<=257;n++) {
        for (size_t align=0;align<32;align++) {
            uint8_t *raw_alloc=malloc(n+align+1),*enc_alloc=malloc(2*n+align+1),*out_alloc=malloc(n+align+1);
            assert(raw_alloc&&enc_alloc&&out_alloc);
            uint8_t *raw=raw_alloc+align+1,*encoded=enc_alloc+align+1,*out=out_alloc+align+1;
            for (size_t i=0;i<n;i++) raw[i]=(uint8_t)(i*73+n);
            for (size_t i=0;i<2*n;i++) encoded[i]=0xa5;
            size_t read=tinygodriver_hex_encode(encoded,2*n,raw,n);
            assert(read==(n&~(size_t)15));
            for (size_t i=0;i<read;i++) {
                assert(encoded[2*i]==alphabet[raw[i]>>4]);
                assert(encoded[2*i+1]==alphabet[raw[i]&15]);
            }
            for (size_t i=2*read;i<2*n;i++) assert(encoded[i]==0xa5);
            for (size_t i=0;i<n;i++) {
                encoded[2*i]=alphabet[raw[i]>>4]; encoded[2*i+1]=alphabet[raw[i]&15]; out[i]=0xa5;
            }
            read=tinygodriver_hex_decode(out,n,encoded,2*n);
            assert(read==((2*n)&~(size_t)31));
            for (size_t i=0;i<read/2;i++) assert(out[i]==raw[i]);
            for (size_t i=read/2;i<n;i++) assert(out[i]==0xa5);
            for (size_t bad=0;bad<2*n;bad++) {
                uint8_t saved=encoded[bad]; encoded[bad]=0x10;
                read=tinygodriver_hex_decode(out,n,encoded,2*n);
                assert(read==(bad&~(size_t)31));
                encoded[bad]=saved;
            }
            free(raw_alloc);free(enc_alloc);free(out_alloc);
        }
    }
    return 0;
}
