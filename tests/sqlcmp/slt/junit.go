package slt

import (
	"encoding/xml"
	"io"
	"time"
)

// JUnitTestSuite is the XML schema most CI systems expect.
// Field tags are XML element names; case-sensitive matching
// per the JUnit XSD.
type JUnitTestSuite struct {
	XMLName  xml.Name        `xml:"testsuite"`
	Name     string          `xml:"name,attr"`
	Tests    int             `xml:"tests,attr"`
	Failures int             `xml:"failures,attr"`
	Errors   int             `xml:"errors,attr"`
	Skipped  int             `xml:"skipped,attr"`
	Time     string          `xml:"time,attr"`
	Cases    []JUnitTestCase `xml:"testcase"`
}

// JUnitTestCase is one test outcome. The Classname is the
// originating file (or "synthetic" for runner-level reports).
type JUnitTestCase struct {
	XMLName   xml.Name      `xml:"testcase"`
	Classname string        `xml:"classname,attr"`
	Name      string        `xml:"name,attr"`
	Time      string        `xml:"time,attr,omitempty"`
	Failure   *JUnitFailure `xml:"failure,omitempty"`
	Skipped   *JUnitSkipped `xml:"skipped,omitempty"`
}

// JUnitFailure describes a non-passing case.
type JUnitFailure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr,omitempty"`
	Body    string `xml:",chardata"`
}

// JUnitSkipped marks a case as deliberately skipped.
type JUnitSkipped struct {
	Message string `xml:"message,attr"`
}

// WriteJUnit emits the given suite as XML to w. Used by
// CI workflows to ingest test results.
func WriteJUnit(w io.Writer, suite JUnitTestSuite) error {
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(suite); err != nil {
		return err
	}
	return enc.Flush()
}

// NewJUnitSuite builds a JUnitTestSuite from per-file stats
// and the overall run totals.
func NewJUnitSuite(files []fileStat, total Stats) JUnitTestSuite {
	suite := JUnitTestSuite{
		Name:     "slt",
		Tests:    len(files),
		Failures: total.Failed,
		Skipped:  total.Skipped,
		Time:     time.Duration(total.Duration).String(),
	}
	for _, f := range files {
		tc := JUnitTestCase{
			Classname: "slt.file",
			Name:      f.path,
			Time:      time.Duration(f.stats.Duration).String(),
		}
		switch {
		case f.stats.Failed > 0:
			tc.Failure = &JUnitFailure{
				Message: "diff mismatch",
				Type:    "DiffMismatch",
				Body:    summaryBody(f),
			}
		case f.stats.Total == f.stats.Skipped+f.stats.ParseErrors:
			tc.Skipped = &JUnitSkipped{Message: "skipped or parse-error"}
		}
		suite.Cases = append(suite.Cases, tc)
	}
	return suite
}

func summaryBody(f fileStat) string {
	return "passed=" + itoa(f.stats.Passed) +
		" failed=" + itoa(f.stats.Failed) +
		" skipped=" + itoa(f.stats.Skipped) +
		" parse-err=" + itoa(f.stats.ParseErrors)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
