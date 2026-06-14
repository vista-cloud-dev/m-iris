package irisdriver

import (
	"testing"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
)

// New must yield a value satisfying the neutral contract without dialing the
// server — this is the seam m-cli's VistaEngine holds. (Construction is lazy;
// the runner deploys on the first verb.)
func TestNew_SatisfiesTransport(t *testing.T) {
	tr, err := New(Config{
		BaseURL:   "https://iris.example:52773/api/atelier/v1/",
		Namespace: "VISTA",
		User:      "_SYSTEM",
		Password:  "SYS",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var _ mdriver.Transport = tr
	if tr == nil {
		t.Fatal("New returned a nil Transport")
	}
}
