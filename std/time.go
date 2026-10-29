package std

import (
	"fmt"
	"strings"
	"time"

	"github.com/fclaude/brote/scheme"
)

func init() { moduleInstallers[Time] = installTime }

var layouts = map[string]string{
	"rfc3339":  time.RFC3339,
	"date":     "2006-01-02",
	"time":     "15:04:05",
	"datetime": "2006-01-02 15:04:05",
}

func zoneArg(c *scheme.Call, i int) (*time.Location, error) {
	if len(c.Args) <= i {
		return time.UTC, nil
	}
	z, err := c.String(i)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(z) {
	case "utc":
		return time.UTC, nil
	case "local":
		return time.Local, nil
	}
	return nil, fmt.Errorf("argument %d: expected \"utc\" or \"local\", got %q", i+1, z)
}

func layoutArg(c *scheme.Call, i int) (string, error) {
	l, err := c.String(i)
	if err != nil {
		return "", err
	}
	if known, ok := layouts[strings.ToLower(l)]; ok {
		return known, nil
	}
	return l, nil
}

func seconds(t time.Time) scheme.Value {
	return scheme.Float(float64(t.UnixNano()) / 1e9)
}

func installTime(s *installer) error {
	max := s.pol.Time.MaxSleep
	zone := scheme.Param{Name: "zone", Type: "string", Doc: `"utc" (the default) or "local"`, Optional: true}
	layout := scheme.Param{Name: "layout", Type: "string", Doc: `"rfc3339", "date", "time", "datetime", or a Go layout such as "Mon Jan 2 15:04"`}
	s.add(&scheme.Prim{
		Name:    "time-now",
		Meta:    scheme.Meta{Doc: "The current time as seconds since 1970-01-01 UTC, a float with the fraction.", TaintOpaque: true},
		Handler: func(c *scheme.Call) (scheme.Value, error) { return seconds(time.Now()), nil },
	})
	s.add(&scheme.Prim{
		Name:    "time-ms",
		Meta:    scheme.Meta{Doc: "The current time as whole milliseconds since 1970-01-01 UTC.", TaintOpaque: true},
		Handler: func(c *scheme.Call) (scheme.Value, error) { return scheme.Int(time.Now().UnixMilli()), nil },
	})
	s.add(&scheme.Prim{
		Name: "time-format",
		Meta: scheme.Meta{Doc: "Seconds since 1970 written as text in a layout and zone.", Params: []scheme.Param{{Name: "seconds", Type: "number"}, layout, zone}},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			n, err := c.Number(0)
			if err != nil {
				return scheme.Value{}, err
			}
			l, err := layoutArg(c, 1)
			if err != nil {
				return scheme.Value{}, err
			}
			loc, err := zoneArg(c, 2)
			if err != nil {
				return scheme.Value{}, err
			}
			f := n.AsFloat()
			t := time.Unix(int64(f), int64((f-float64(int64(f)))*1e9)).In(loc)
			return scheme.Str(t.Format(l)), nil
		},
	})
	s.add(&scheme.Prim{
		Name: "time-parse",
		Meta: scheme.Meta{Doc: "Text in a layout and zone as seconds since 1970 (a float), or an error if the text does not fit. A zone in the text itself wins.", Params: []scheme.Param{{Name: "text", Type: "string"}, layout, zone}},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			text, err := c.String(0)
			if err != nil {
				return scheme.Value{}, err
			}
			l, err := layoutArg(c, 1)
			if err != nil {
				return scheme.Value{}, err
			}
			loc, err := zoneArg(c, 2)
			if err != nil {
				return scheme.Value{}, err
			}
			t, err := time.ParseInLocation(l, text, loc)
			if err != nil {
				return scheme.Value{}, err
			}
			return seconds(t), nil
		},
	})
	s.add(&scheme.Prim{
		Name: "time-sleep",
		Meta: scheme.Meta{
			Doc:    fmt.Sprintf("Pause for a number of seconds (a fraction is fine). The host allows at most %s per call. Stops early if the evaluation is cancelled.", max),
			Params: []scheme.Param{{Name: "seconds", Type: "number"}},
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			n, err := c.Number(0)
			if err != nil {
				return scheme.Value{}, err
			}
			f := n.AsFloat()
			if f < 0 || f != f {
				return scheme.Value{}, fmt.Errorf("argument 1: expected a non-negative number, got %s", scheme.Write(n))
			}
			if f > max.Seconds() {
				return scheme.Value{}, fmt.Errorf("%s seconds is more than the %s this host allows", scheme.Write(n), max)
			}
			t := time.NewTimer(time.Duration(f * float64(time.Second)))
			defer t.Stop()
			select {
			case <-t.C:
				return scheme.Void, nil
			case <-c.Ctx.Done():
				return scheme.Value{}, c.Ctx.Err()
			}
		},
	})
	return nil
}
