package wyze

import (
	"testing"

	"github.com/AlexxIT/go2rtc/pkg/tutk"
	"github.com/stretchr/testify/require"
)

// TestGenerateChallengeResponse pins the three authentication status branches
// (default fixed key, 16-char ENR, 32-char ENR with pre-decrypt) so a future
// refactor can't silently swap them — an auth regression would lock every user
// out of their cameras.
func TestGenerateChallengeResponse(t *testing.T) {
	challenge := []byte("0123456789ABCDEF") // 16 bytes
	enr := "ABCDEFGHIJKLMNOPQRSTUVWXYZ012345"

	t.Run("status 1 uses fixed FFFF key", func(t *testing.T) {
		got := generateChallengeResponse(challenge, enr, statusDefault)
		want := tutk.XXTEADecryptVar(challenge, []byte("FFFFFFFFFFFFFFFF"))
		require.Equal(t, want, got)
	})

	t.Run("status 3 uses first 16 ENR chars", func(t *testing.T) {
		got := generateChallengeResponse(challenge, enr, statusENR16)
		want := tutk.XXTEADecryptVar(challenge, []byte(enr[:16]))
		require.Equal(t, want, got)
	})

	t.Run("status 6 pre-decrypts with enr[:16] then keys with enr[16:32]", func(t *testing.T) {
		got := generateChallengeResponse(challenge, enr, statusENR32)
		pre := tutk.XXTEADecryptVar(challenge, []byte(enr[:16]))
		want := tutk.XXTEADecryptVar(pre, []byte(enr[16:32]))
		require.Equal(t, want, got)
	})
}

func TestHashPassword(t *testing.T) {
	// md5: prefix is passed through verbatim (already triple-hashed).
	require.Equal(t, "deadbeef", hashPassword("md5:deadbeef"))
	require.Equal(t, "deadbeef", hashPassword("MD5:deadbeef"))

	// Plain password is triple-MD5 hashed to 32 lowercase hex chars.
	got := hashPassword("hunter2")
	require.Len(t, got, 32)
	require.Regexp(t, "^[0-9a-f]{32}$", got)
	// Deterministic.
	require.Equal(t, got, hashPassword("hunter2"))
}
