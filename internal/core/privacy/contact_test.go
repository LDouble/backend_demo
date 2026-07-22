package privacy

import "testing"

func TestMaskContact(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "phone", value: "13800000000", want: "1*********0"},
		{name: "two runes", value: "QQ", want: "**"},
		{name: "unicode", value: "微信号", want: "微*号"},
		{name: "trims whitespace", value: "  abc  ", want: "a*c"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := MaskContact(test.value); got != test.want {
				t.Fatalf("MaskContact(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}
