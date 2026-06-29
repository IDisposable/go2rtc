package tutk

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestXXTEADecrypt(t *testing.T) {
	buf := []byte("WERhJxb87WF3zgPa")
	key := []byte("GAgDiwVPg2E4GMke")
	XXTEADecrypt(buf, buf, key)
	require.Equal(t, "\xc4\xa6\x2c\xa1\x10\x64\x17\xa5\xda\x02\xe1\x62\xa5\xf0\x62\x71", string(buf))
}

// TestTransCodePartialRoundTrip checks that the Reverse (decode) and forward
// (encode) TransCode are exact inverses across the 16-byte block boundary and
// the short-tail path. This is the obfuscation every TUTK packet rides on, so
// any regression here silently corrupts the whole stream.
func TestTransCodePartialRoundTrip(t *testing.T) {
	// Sizes chosen to exercise full blocks plus every tail length that has a
	// dedicated swap case (2, 4, 8) and a generic-copy tail (3, 7).
	for _, n := range []int{1, 2, 3, 4, 7, 8, 15, 16, 17, 31, 32, 48, 100} {
		src := make([]byte, n)
		for i := range src {
			src[i] = byte(i*7 + 3)
		}

		enc := TransCodePartial(nil, src)
		dec := ReverseTransCodePartial(nil, enc)
		require.Equal(t, src, dec, "ReverseTransCodePartial∘TransCodePartial mismatch at n=%d", n)

		// Mutual inverse the other way too.
		enc2 := ReverseTransCodePartial(nil, src)
		dec2 := TransCodePartial(nil, enc2)
		require.Equal(t, src, dec2, "TransCodePartial∘ReverseTransCodePartial mismatch at n=%d", n)
	}
}

// TestTransCodeBlobRoundTrip covers the header-driven partial/full encryption
// split used for real packets (the dst[3]&1 branch and the 48/64-byte
// boundaries).
func TestTransCodeBlobRoundTrip(t *testing.T) {
	for _, n := range []int{1, 16, 17, 48, 64, 65, 200} {
		for _, parity := range []byte{0, 1} {
			src := make([]byte, n)
			for i := range src {
				src[i] = byte(i*13 + 5)
			}
			if n > 3 {
				// Header byte 3 low bit selects partial vs full encryption.
				src[3] = (src[3] &^ 1) | parity
			}

			enc := TransCodeBlob(src)
			dec := ReverseTransCodeBlob(enc)
			require.Equal(t, src, dec, "blob round-trip mismatch n=%d parity=%d", n, parity)
			require.False(t, n >= 16 && bytes.Equal(src, enc), "blob should transform data n=%d", n)
		}
	}
}
