package main

import "testing"

func TestPublicListenerDetection(t *testing.T) {
	for _, test := range []struct {
		address string
		public  bool
	}{
		{address: ":25775", public: true},
		{address: "0.0.0.0:25775", public: true},
		{address: "[::]:25775", public: true},
		{address: "127.0.0.1:25775", public: false},
		{address: "[::1]:25775", public: false},
		{address: "localhost:25775", public: true},
	} {
		if got := isPublicListener(test.address); got != test.public {
			t.Fatalf("isPublicListener(%q) = %v, want %v", test.address, got, test.public)
		}
	}
}
