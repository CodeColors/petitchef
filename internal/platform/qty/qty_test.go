package qty

import "testing"

func TestParseDecimal(t *testing.T) {
	for in, want := range map[string]int64{
		"2": 2000, "1,5": 1500, "0.25": 250, "½": 500, "1/2": 500, "1 ½": 1500, "1½": 1500,
		"1 1/2": 1500, "1/3": 333, ",5": 500, "250": 250_000, "3,1416": 3141,
	} {
		got, err := ParseDecimal(in)
		if err != nil || got != want {
			t.Errorf("ParseDecimal(%q) = %d, %v ; attendu %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "abc", "1/0", "-2", "0", "1 2 3", "1,a"} {
		if _, err := ParseDecimal(in); err == nil {
			t.Errorf("ParseDecimal(%q) : erreur attendue", in)
		}
	}
}

func TestFormat(t *testing.T) {
	for _, c := range []struct {
		v    int64
		b    Base
		want string
	}{
		{250, Gram, "250 g"}, {1250, Gram, "1,25 kg"}, {1000, Gram, "1 kg"},
		{750, Milli, "75 cl"}, {1500, Milli, "1,5 L"}, {15, Milli, "15 ml"},
		{500, Piece, "½ pièce"}, {1500, Piece, "1 ½ pièce"}, {3000, Piece, "3 pièces"},
		{330, Piece, "0,33 pièce"},
	} {
		if got := Format(c.v, c.b); got != c.want {
			t.Errorf("Format(%d, %s) = %q ; attendu %q", c.v, c.b, got, c.want)
		}
	}
}

func TestCanonical(t *testing.T) {
	for in, want := range map[string]string{
		"c. à soupe": "cs", "cuillères à soupe": "cs", "càs": "cs", "CS": "cs", "c.à.c.": "cc",
		"cuillère à café": "cc", "Gousses": "gousse", "kg": "kg", "L": "l", "pincées": "pincee", "truc": "",
	} {
		if got := Canonical(in); got != want {
			t.Errorf("Canonical(%q) = %q ; attendu %q", in, got, want)
		}
	}
}
