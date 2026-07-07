package router

import (
	"testing"

	"tokenrouter/internal/classify"
)

func TestLooselyAgrees(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{
			"agreeing factual answers",
			"The Earth has seasons because its axis is tilted about 23.5 degrees, changing how directly sunlight hits each hemisphere.",
			"Seasons occur due to the axial tilt of the Earth (23.5 degrees), which changes how much direct sunlight each hemisphere receives during the orbit.",
			true,
		},
		{
			"contradicting puzzle answers",
			"A is a knave and B is a knight, because the statement must be false.",
			"Both A and B could be knaves; the statement gives no information about B.",
			false,
		},
		{
			"number disagreement",
			"The population will be 54080 after two years.",
			"After two years the population reaches 54000.",
			false,
		},
		{
			"same number different words",
			"The answer is 42.",
			"It equals 42.",
			true,
		},
	}
	for _, c := range cases {
		if got := looselyAgrees(c.a, c.b); got != c.want {
			t.Errorf("%s: looselyAgrees = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestNormalizeAnswerMath(t *testing.T) {
	if got := normalizeAnswer(classify.Math, "The total is $1,320."); got != "1320" {
		t.Errorf("normalizeAnswer math = %q, want 1320", got)
	}
}
