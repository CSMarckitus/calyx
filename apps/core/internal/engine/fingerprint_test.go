package engine

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestSingleFingerprintGivesTheDigestOfTheWorkedExample(t *testing.T) {
	// bytes: 06 "single" 0b "34600111222" 05 "Hello"
	const want = "2e1a3ade460f5ff945f4d389a16a9bf76281223390bef019bb39c712e740b9cc"
	if got := hex.EncodeToString(singleFingerprint("34600111222", "Hello")); got != want {
		t.Fatalf("digest %s, want %s", got, want)
	}
}

func TestSingleFingerprintTrimsTheMessageText(t *testing.T) {
	if !bytes.Equal(singleFingerprint("34600111222", " Hello \n"), singleFingerprint("34600111222", "Hello")) {
		t.Fatal("an untrimmed message text gives another digest")
	}
}

func sameInput() []BulkMessageInput {
	return []BulkMessageInput{{To: "34600111222", Text: " Hi "}, {To: "34600333444", Text: " Hi "}}
}

func samePacing() BulkPacingOptions {
	return BulkPacingOptions{Delay: &Range{Min: 3000, Max: 6000}}
}

func customInput() []BulkMessageInput {
	delay := 5000
	return []BulkMessageInput{{To: "34600111222", Text: "Hi", DelayMs: &delay}, {To: "34600333444", Text: "Bye"}}
}

// The bytes of the two pinned digests, written out by hand from the fingerprint encoding
// ("i64" is a field of 8 bytes: 08 then the big-endian value; presence is a field of 1 byte):
//
//	same:   09 "bulk-same" 08 i64(2) 0b "34600111222" 0b "34600333444" 02 "Hi"
//	        01 01 08 i64(3000) 08 i64(6000)   01 00   01 00
//	custom: 0b "bulk-custom" 08 i64(2)
//	        0b "34600111222" 02 "Hi" 01 01 08 i64(5000)
//	        0b "34600333444" 03 "Bye" 01 00
//	        01 00   01 00   01 00
func TestBulkFingerprintGivesThePinnedDigests(t *testing.T) {
	cases := []struct {
		name string
		got  []byte
		want string
	}{
		{"same", bulkFingerprint(BulkSame, sameInput(), samePacing()),
			"bfc6394b6899ffd346caf2d1b8f7ae629b7da97d95e78b2491acef47f702641d"},
		{"custom", bulkFingerprint(BulkCustom, customInput(), BulkPacingOptions{}),
			"15d3f970b32a2e81c9df2ecfb07c10ab62da5bfc0219579fa41ae22a965fe98a"},
	}
	for _, c := range cases {
		if got := hex.EncodeToString(c.got); got != c.want {
			t.Errorf("%s: digest %s, want %s", c.name, got, c.want)
		}
	}
}

func TestBulkFingerprintChangesWithTheKind(t *testing.T) {
	if bytes.Equal(bulkFingerprint(BulkSame, sameInput(), samePacing()), bulkFingerprint(BulkCustom, sameInput(), samePacing())) {
		t.Fatal("the kind does not change the digest")
	}
}

func TestBulkFingerprintChangesWithTheOrderOfTheMessages(t *testing.T) {
	messages := customInput()
	swapped := []BulkMessageInput{messages[1], messages[0]}
	if bytes.Equal(bulkFingerprint(BulkCustom, messages, BulkPacingOptions{}), bulkFingerprint(BulkCustom, swapped, BulkPacingOptions{})) {
		t.Fatal("the order of the messages does not change the digest")
	}
}

func TestBulkFingerprintTellsAnAbsentDelayFromAZeroDelay(t *testing.T) {
	zero := 0
	absent := []BulkMessageInput{{To: "34600111222", Text: "Hi"}}
	present := []BulkMessageInput{{To: "34600111222", Text: "Hi", DelayMs: &zero}}
	if bytes.Equal(bulkFingerprint(BulkCustom, absent, BulkPacingOptions{}), bulkFingerprint(BulkCustom, present, BulkPacingOptions{})) {
		t.Fatal("an absent delay and a delay of 0 give the same digest")
	}
}
