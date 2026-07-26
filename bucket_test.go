package featureflags

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

type bucketVectorFile struct {
	Algorithm   string         `json:"algorithm"`
	ByteOrder   string         `json:"byteOrder"`
	Encoding    string         `json:"encoding"`
	FieldLength string         `json:"fieldLength"`
	Precision   uint64         `json:"precision"`
	Prefix      string         `json:"prefix"`
	Version     int            `json:"version"`
	Vectors     []bucketVector `json:"vectors"`
}

type bucketVector struct {
	Name       string `json:"name"`
	Seed       string `json:"seed"`
	FeatureKey string `json:"featureKey"`
	Tenant     string `json:"tenant"`
	Subject    string `json:"subject"`
	SHA256     string `json:"sha256"`
	Bucket     uint32 `json:"bucket"`
}

func TestBucketStableVectors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		seed       string
		featureKey string
		tenant     string
		subject    string
		want       uint32
	}{
		{name: "default seed", featureKey: "checkout.redesign", tenant: "tenant-a", subject: "user-123", want: 51_295},
		{name: "explicit seed", seed: "experiment-7", featureKey: "search.rank", tenant: "tenant-a", subject: "user-123", want: 26_947},
		{name: "tenant isolation", seed: "experiment-7", featureKey: "search.rank", tenant: "tenant-b", subject: "user-123", want: 17_802},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := Bucket(test.seed, test.featureKey, test.tenant, test.subject); got != test.want {
				t.Fatalf("Bucket() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestBucketPortableCompatibilityVectors(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("testdata/bucketing-v1.json")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var fixture bucketVectorFile
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if fixture.Version != 1 || fixture.Algorithm != "sha256-first-uint64-modulo" ||
		fixture.ByteOrder != "big-endian" || fixture.Encoding != "utf-8" ||
		fixture.FieldLength != "uint32-big-endian-bytes" ||
		fixture.Precision != bucketPrecision || fixture.Prefix != "go-feature-flags/bucket/v1" {
		t.Fatalf("bucketing contract metadata changed: %#v", fixture)
	}
	if len(fixture.Vectors) == 0 {
		t.Fatal("bucketing fixture has no vectors")
	}
	for _, vector := range fixture.Vectors {
		t.Run(vector.Name, func(t *testing.T) {
			t.Parallel()

			digest := portableBucketDigest(fixture.Prefix, vector)
			if got := hex.EncodeToString(digest[:]); got != vector.SHA256 {
				t.Fatalf("digest = %s, want %s", got, vector.SHA256)
			}
			if got := Bucket(vector.Seed, vector.FeatureKey, vector.Tenant, vector.Subject); got != vector.Bucket {
				t.Fatalf("Bucket() = %d, want %d", got, vector.Bucket)
			}
		})
	}
}

func portableBucketDigest(prefix string, vector bucketVector) [sha256.Size]byte {
	data := []byte(prefix)
	for _, field := range []string{vector.Seed, vector.FeatureKey, vector.Tenant, vector.Subject} {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len([]byte(field))))
		data = append(data, length[:]...)
		data = append(data, []byte(field)...)
	}

	return sha256.Sum256(data)
}
