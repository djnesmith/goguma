package install

// Signature is the platform-independent shape; on Linux there is nothing that
// plays the part macOS code signing does for a locally installed binary, so
// this reports nothing rather than inventing a guarantee.
type Signature struct {
	Valid     bool
	Authority string
	AdHoc     bool
}

func (s Signature) Describe() string { return "" }

func checkSignature(string) (Signature, error) { return Signature{}, nil }
