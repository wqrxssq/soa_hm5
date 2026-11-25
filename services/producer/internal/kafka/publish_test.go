package kafka

import "testing"

func TestRecordKeyIsUserID(t *testing.T) {
	record := NewRecord("user-01", []byte("payload"))
	if string(record.Key) != "user-01" {
		t.Fatalf("key %q", record.Key)
	}
	if record.Topic != Topic {
		t.Fatalf("topic %s", record.Topic)
	}
}
