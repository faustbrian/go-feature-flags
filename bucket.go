package featureflags

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
)

const bucketPrecision = uint64(100_000)

// Bucket returns a stable tenant-scoped assignment in [0, 100000). Its input
// framing and SHA-256 mapping are part of the package compatibility contract.
func Bucket(seed, featureKey, tenant, subject string) uint32 {
	digest := sha256.New()
	_, _ = digest.Write([]byte("go-feature-flags/bucket/v1"))
	writeBucketPart(digest, seed)
	writeBucketPart(digest, featureKey)
	writeBucketPart(digest, tenant)
	writeBucketPart(digest, subject)
	sum := digest.Sum(nil)

	return uint32(binary.BigEndian.Uint64(sum[:8]) % bucketPrecision)
}

func writeBucketPart(digest hash.Hash, part string) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(part)))
	_, _ = digest.Write(length[:])
	_, _ = digest.Write([]byte(part))
}
