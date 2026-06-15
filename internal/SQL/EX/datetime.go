package EX

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// datetime.go implements SQL date/time types and functions (REQ000262+263).
// Date/time values are stored as strings in ISO 8601 format internally
// and converted to time.Time when needed for arithmetic or functions.

var datetimeLayouts = []string{
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02",
	"15:04:05",
	"2006-01-02 15:04:05.000",
	"2006-01-02T15:04:05.000",
	time.RFC3339,
}

// ParseDateTime attempts to parse a string as a date/time value.
func ParseDateTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range datetimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// FormatDateTimeValue formats a time.Time for display based on SQL type.
func FormatDateTimeValue(t time.Time, typ int) string {
	switch typ {
	case 73: // T_TIMESTAMP
		return t.Format("2006-01-02 15:04:05")
	case 75: // T_DATE
		return t.Format("2006-01-02")
	case 76: // T_TIME
		return t.Format("15:04:05")
	default:
		return t.Format("2006-01-02 15:04:05")
	}
}

// toTime converts an interface{} to time.Time.
// Supports string (parsed), time.Time (direct), int64/float64 (unix timestamp).
func toTime(v interface{}) (time.Time, bool) {
	switch val := v.(type) {
	case time.Time:
		return val, true
	case string:
		if t, ok := ParseDateTime(val); ok {
			return t, true
		}
		return time.Time{}, false
	case int64:
		return time.Unix(val, 0).UTC(), true
	case float64:
		sec := int64(val)
		nsec := int64((val - float64(sec)) * 1e9)
		return time.Unix(sec, nsec).UTC(), true
	default:
		return time.Time{}, false
	}
}

// strftimeFormat implements SQLite's strftime() formatting.
func strftimeFormat(t time.Time, format string) string {
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		ch := format[i]
		if ch != '%' {
			b.WriteByte(ch)
			continue
		}
		if i+1 >= len(format) {
			b.WriteByte(ch)
			continue
		}
		i++
		switch format[i] {
		case 'Y':
			b.WriteString(strconv.Itoa(t.Year()))
		case 'm':
			b.WriteString(fmt.Sprintf("%02d", t.Month()))
		case 'd':
			b.WriteString(fmt.Sprintf("%02d", t.Day()))
		case 'H':
			b.WriteString(fmt.Sprintf("%02d", t.Hour()))
		case 'M':
			b.WriteString(fmt.Sprintf("%02d", t.Minute()))
		case 'S':
			b.WriteString(fmt.Sprintf("%02d", t.Second()))
		case 'w':
			b.WriteString(strconv.Itoa(int(t.Weekday())))
		case 'j':
			b.WriteString(fmt.Sprintf("%03d", t.YearDay()))
		case 'U':
			_, w := t.ISOWeek()
			b.WriteString(fmt.Sprintf("%02d", w))
		case 'f':
			b.WriteString(fmt.Sprintf("%06d", t.Nanosecond()/1000))
		case 'P':
			if t.Hour() < 12 {
				b.WriteString("AM")
			} else {
				b.WriteString("PM")
			}
		case 'p':
			if t.Hour() < 12 {
				b.WriteString("am")
			} else {
				b.WriteString("pm")
			}
		case 'I':
			h := t.Hour() % 12
			if h == 0 {
				h = 12
			}
			b.WriteString(fmt.Sprintf("%02d", h))
		case '%':
			b.WriteByte('%')
		default:
			b.WriteByte('%')
			b.WriteByte(format[i])
		}
	}
	return b.String()
}

// julianDay converts a time.Time to Julian Day Number (floating point).
func julianDay(t time.Time) float64 {
	y := t.Year()
	m := int(t.Month())
	d := t.Day()

	if m <= 2 {
		y--
		m += 12
	}
	A := y / 100
	B := 2 - A + A/4

	dayNum := float64(int(365.25*float64(y+4716))) +
		float64(int(30.6001*float64(m+1))) +
		float64(d) + float64(B) - 1524.5
	frac := (float64(t.Hour()) + float64(t.Minute())/60.0 + float64(t.Second())/3600.0) / 24.0
	return dayNum + frac
}

// DateAdd adds an interval to a date/time value.
func DateAdd(t time.Time, amount int, unit string) time.Time {
	switch strings.ToUpper(unit) {
	case "YEAR", "YEARS":
		return t.AddDate(amount, 0, 0)
	case "MONTH", "MONTHS":
		return t.AddDate(0, amount, 0)
	case "DAY", "DAYS":
		return t.AddDate(0, 0, amount)
	case "HOUR", "HOURS":
		return t.Add(time.Duration(amount) * time.Hour)
	case "MINUTE", "MINUTES":
		return t.Add(time.Duration(amount) * time.Minute)
	case "SECOND", "SECONDS":
		return t.Add(time.Duration(amount) * time.Second)
	default:
		return t
	}
}

// DateSub subtracts an interval from a date/time value.
func DateSub(t time.Time, amount int, unit string) time.Time {
	return DateAdd(t, -amount, unit)
}

// DateDiff returns the difference between two dates in the given unit.
func DateDiff(a, b time.Time, unit string) int64 {
	switch strings.ToUpper(unit) {
	case "YEAR", "YEARS":
		return int64(a.Year() - b.Year())
	case "MONTH", "MONTHS":
		return int64(a.Year()-b.Year())*12 + int64(a.Month()-b.Month())
	case "DAY", "DAYS":
		return int64(a.Sub(b).Hours() / 24)
	case "HOUR", "HOURS":
		return int64(a.Sub(b).Hours())
	case "MINUTE", "MINUTES":
		return int64(a.Sub(b).Minutes())
	case "SECOND", "SECONDS":
		return int64(a.Sub(b).Seconds())
	default:
		return int64(a.Sub(b).Seconds())
	}
}

// evalDateTimeFunc evaluates SQL date/time functions.
func evalDateTimeFunc(name string, args []interface{}) (interface{}, error) {
	switch strings.ToUpper(name) {
	case "DATE":
		if len(args) == 0 {
			return time.Now().UTC().Format("2006-01-02"), nil
		}
		t, ok := toTime(args[0])
		if !ok {
			return nil, fmt.Errorf("date(): invalid argument")
		}
		return t.Format("2006-01-02"), nil

	case "TIME":
		if len(args) == 0 {
			return time.Now().UTC().Format("15:04:05"), nil
		}
		t, ok := toTime(args[0])
		if !ok {
			return nil, fmt.Errorf("time(): invalid argument")
		}
		return t.Format("15:04:05"), nil

	case "DATETIME":
		if len(args) == 0 {
			return time.Now().UTC().Format("2006-01-02 15:04:05"), nil
		}
		t, ok := toTime(args[0])
		if !ok {
			return nil, fmt.Errorf("datetime(): invalid argument")
		}
		return t.Format("2006-01-02 15:04:05"), nil

	case "JULIANDAY":
		if len(args) == 0 {
			return julianDay(time.Now().UTC()), nil
		}
		t, ok := toTime(args[0])
		if !ok {
			return nil, fmt.Errorf("julianday(): invalid argument")
		}
		return julianDay(t), nil

	case "STRFTIME":
		if len(args) < 2 {
			return nil, fmt.Errorf("strftime(): requires at least 2 arguments")
		}
		format, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("strftime(): format must be a string")
		}
		t, ok := toTime(args[1])
		if !ok {
			return nil, fmt.Errorf("strftime(): invalid time argument")
		}
		return strftimeFormat(t, format), nil

	case "EXTRACT":
		if len(args) < 2 {
			return nil, fmt.Errorf("extract(): requires 2 arguments")
		}
		field, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("extract(): field must be a string")
		}
		t, ok := toTime(args[1])
		if !ok {
			return nil, fmt.Errorf("extract(): invalid time argument")
		}
		return evalExtractField(field, t)

	case "NOW", "CURRENT_TIMESTAMP":
		return time.Now().UTC().Format("2006-01-02 15:04:05"), nil

	case "CURRENT_DATE":
		return time.Now().UTC().Format("2006-01-02"), nil

	case "CURRENT_TIME":
		return time.Now().UTC().Format("15:04:05"), nil

	default:
		return nil, fmt.Errorf("unknown datetime function: %s", name)
	}
}

func evalExtractField(field string, t time.Time) (int64, error) {
	switch strings.ToUpper(field) {
	case "YEAR":
		return int64(t.Year()), nil
	case "MONTH":
		return int64(t.Month()), nil
	case "DAY":
		return int64(t.Day()), nil
	case "HOUR":
		return int64(t.Hour()), nil
	case "MINUTE":
		return int64(t.Minute()), nil
	case "SECOND":
		return int64(t.Second()), nil
	case "DOW", "DAYOFWEEK":
		return int64(t.Weekday()), nil
	case "DOY", "DAYOFYEAR":
		return int64(t.YearDay()), nil
	case "WEEK":
		_, w := t.ISOWeek()
		return int64(w), nil
	default:
		return 0, fmt.Errorf("extract(): unknown field %s", field)
	}
}

// isDateTimeFunc returns true if the function name is a datetime function.
func isDateTimeFunc(name string) bool {
	switch strings.ToUpper(name) {
	case "DATE", "TIME", "DATETIME", "JULIANDAY", "STRFTIME",
		"EXTRACT", "NOW", "CURRENT_TIMESTAMP", "CURRENT_DATE", "CURRENT_TIME":
		return true
	}
	return false
}

// DateTimeArithmetic handles date +/- interval and date - date operations.
func DateTimeArithmetic(left interface{}, right interface{}, op string) (interface{}, error) {
	switch op {
	case "+":
		return dateTimeAdd(left, right)
	case "-":
		return dateTimeSub(left, right)
	}
	return nil, fmt.Errorf("unsupported datetime operation: %s", op)
}

func dateTimeAdd(left interface{}, right interface{}) (interface{}, error) {
	lt, lok := toTime(left)
	if !lok {
		return nil, nil
	}
	switch r := right.(type) {
	case *IntervalValue:
		result := DateAdd(lt, r.Amount, r.Unit)
		return result.Format("2006-01-02 15:04:05"), nil
	}
	return nil, nil
}

func dateTimeSub(left interface{}, right interface{}) (interface{}, error) {
	lt, lok := toTime(left)
	if !lok {
		return nil, nil
	}
	switch r := right.(type) {
	case *IntervalValue:
		result := DateSub(lt, r.Amount, r.Unit)
		return result.Format("2006-01-02 15:04:05"), nil
	}
	rt, rok := toTime(right)
	if rok {
		days := DateDiff(lt, rt, "DAY")
		return days, nil
	}
	return nil, nil
}

// IntervalValue represents a parsed INTERVAL value.
type IntervalValue struct {
	Amount int
	Unit   string
}

// ParseInterval parses an INTERVAL literal like INTERVAL '7' DAY.
func ParseInterval(s string) (int, string, bool) {
	s = strings.TrimSpace(s)
	parts := strings.Fields(s)
	if len(parts) != 2 {
		return 0, "", false
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, "", false
	}
	unit := strings.ToUpper(parts[1])
	validUnits := map[string]bool{
		"YEAR": true, "YEARS": true,
		"MONTH": true, "MONTHS": true,
		"DAY": true, "DAYS": true,
		"HOUR": true, "HOURS": true,
		"MINUTE": true, "MINUTES": true,
		"SECOND": true, "SECONDS": true,
	}
	if !validUnits[unit] {
		return 0, "", false
	}
	return n, unit, true
}
