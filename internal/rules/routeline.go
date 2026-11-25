package rules

import (
	"fmt"
	"strings"
)

// A route line can be changed from the window: it is read out of the text,
// changed, and written back in its place, with the comment after it kept. The
// rest of the text is left as it was.

// RouteLine is what a route line says.
type RouteLine struct {
	ID, Kind, Arg string // Arg: the region, the expression, or the route followed
	Fast, Gate    bool
	IPv4          bool
	Exit          []string
	Check, Hours  string // as written: "5m", "09:00-23:30"; "" when not given
}

// ReadRoute finds the line of route id in text.
func ReadRoute(text, id string) (RouteLine, bool) {
	_, rl, ok := findRoute(strings.Split(text, "\n"), id)
	return rl, ok
}

// SetRoute writes rl over the line of its route in text.
func SetRoute(text string, rl RouteLine) (string, error) {
	lines := strings.Split(text, "\n")
	i, _, ok := findRoute(lines, rl.ID)
	if !ok {
		return "", fmt.Errorf("route %s is not in the rules", rl.ID)
	}
	line := rl.String()
	if c := comment(lines[i]); c != "" {
		line += " " + c
	}
	lines[i] = line
	return strings.Join(lines, "\n"), nil
}

func findRoute(lines []string, id string) (int, RouteLine, bool) {
	for i, raw := range lines {
		f := fields(stripComment(raw))
		if len(f) >= 4 && f[0] == "route" && f[1] == id {
			rl := RouteLine{ID: id, Kind: f[2], Arg: f[3]}
			for _, m := range f[4:] {
				switch {
				case m == "fast":
					rl.Fast = true
				case m == "gate":
					rl.Gate = true
				case m == "ipv4":
					rl.IPv4 = true
				case strings.HasPrefix(m, "exit:"):
					rl.Exit = append(rl.Exit, m[len("exit:"):])
				case strings.HasPrefix(m, "check:"):
					rl.Check = m[len("check:"):]
				case strings.HasPrefix(m, "hours:"):
					rl.Hours = m[len("hours:"):]
				}
			}
			return i, rl, true
		}
	}
	return 0, RouteLine{}, false
}

// String is the line, in the order the options are written in.
func (rl RouteLine) String() string {
	arg := rl.Arg
	if rl.Kind == "regex" {
		arg = `"` + arg + `"` // as written: quotes are dropped when read, nothing is unescaped
	}
	out := []string{"route", rl.ID, rl.Kind, arg}
	if rl.Fast {
		out = append(out, "fast")
	}
	if rl.Gate {
		out = append(out, "gate")
	}
	if rl.IPv4 {
		out = append(out, "ipv4")
	}
	for _, x := range rl.Exit {
		out = append(out, "exit:"+x)
	}
	if rl.Check != "" && len(rl.Exit) > 0 {
		out = append(out, "check:"+rl.Check)
	}
	if rl.Hours != "" && len(rl.Exit) > 0 {
		out = append(out, "hours:"+rl.Hours)
	}
	return strings.Join(out, " ")
}

// comment is the comment at the end of a line, "# ..." with its mark, or "".
func comment(line string) string {
	body := stripComment(line)
	return strings.TrimSpace(line[len(body):])
}
