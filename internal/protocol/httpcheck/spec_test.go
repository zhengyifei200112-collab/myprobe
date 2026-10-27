package httpcheck

import "testing"

func TestSpecValidationBoundaries(t *testing.T) {
	valid := Spec{URL: "https://example.com/health", Method: "GET", StatusCodes: []int{200, 204}, TimeoutMS: 5000, MaxRedirects: 3, MaxBodyBytes: 1 << 20}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"file:///etc/passwd", "https://user:password@example.com", "https://example.com/#fragment", "https://example.com/#", "https://example.com/\n", "//example.com", "https:example.com"} {
		copy := valid
		copy.URL = input
		if copy.Validate() == nil {
			t.Errorf("accepted URL %q", input)
		}
	}
	for _, mutate := range []func(*Spec){
		func(s *Spec) { s.Method = "POST" }, func(s *Spec) { s.StatusCodes = []int{200, 200} }, func(s *Spec) { s.StatusCodes = []int{600} }, func(s *Spec) { s.StatusCodes = nil },
		func(s *Spec) { s.TimeoutMS = 99 }, func(s *Spec) { s.TimeoutMS = 60001 }, func(s *Spec) { s.MaxRedirects = 4 }, func(s *Spec) { s.MaxBodyBytes = 1<<20 + 1 },
	} {
		copy := valid
		mutate(&copy)
		if copy.Validate() == nil {
			t.Errorf("accepted invalid spec: %+v", copy)
		}
	}
}
