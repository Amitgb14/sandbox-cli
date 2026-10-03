package testdata

// Inner is promoted into Outer by encoding/json.
type Inner struct {
	Field string `json:"field"`
}

// Outer embeds it, which the generator must refuse rather than silently drop.
type Outer struct {
	Inner
	Own string `json:"own"`
}
