package distribution

import "testing"

func TestRoundTrip(t *testing.T) {
	base := []byte("MZ fake executable payload")
	want := Config{Server: "https://status.example.com"}
	data, err := Encode(base, want)
	if err != nil {
		t.Fatal(err)
	}
	if string(data[:len(base)]) != string(base) {
		t.Fatal("base binary changed")
	}
	got, ok := Decode(data)
	if !ok || got != want {
		t.Fatalf("decoded %+v, %v", got, ok)
	}
	if _, ok := Decode(base); ok {
		t.Fatal("plain binary contains a distribution marker")
	}
	trailing := append(append([]byte(nil), data...), []byte("x")...)
	if _, ok := Decode(trailing); ok {
		t.Fatal("trailing bytes accepted")
	}
}
