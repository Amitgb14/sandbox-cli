package testdata

// Thing references a type this file does not declare, which the generator must
// refuse rather than emit as a bare name.
type Thing struct {
	Probe Missing `json:"probe"`
}
