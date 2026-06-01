package pii

import (
	"regexp"
	"strings"
)

type Scanner struct {
	Name    string
	Pattern *regexp.Regexp
	Mask    string
}

var DefaultScanners = []Scanner{
	{
		Name:    "Credit Card",
		Pattern: regexp.MustCompile(`\b(?:4[0-9]{3}[- ]?[0-9]{4}[- ]?[0-9]{4}[- ]?[0-9]{4}|(?:4[0-9]{12}(?:[0-9]{3})?)|5[1-5][0-9]{2}[- ]?[0-9]{4}[- ]?[0-9]{4}[- ]?[0-9]{4}|6(?:011|5[0-9][0-9])[- ]?[0-9]{4}[- ]?[0-9]{4}[- ]?[0-9]{4}|3[47][0-9]{2}[- ]?[0-9]{4}[- ]?[0-9]{4}[- ]?[0-9]{4}|3(?:0[0-5]|[68][0-9])[0-9][- ]?[0-9]{4}[- ]?[0-9]{4}[- ]?[0-9]{4}|(?:2131|1800|35\d{3})\d{11})\b`),
		Mask:    "****-****-****-****",
	},
	{
		Name:    "SSN",
		Pattern: regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
		Mask:    "***-**-****",
	},
	{
		Name:    "IPv4",
		Pattern: regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`),
		Mask:    "*.*.*.*",
	},
	{
		Name:    "IPv6",
		Pattern: regexp.MustCompile(`\b(?:[0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}\b`),
		Mask:    "****:****:****:****:****:****:****:****",
	},
	{
		Name:    "Email",
		Pattern: regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Z|a-z]{2,}\b`),
		Mask:    "****@****.***",
	},
	{
		Name:    "Phone",
		Pattern: regexp.MustCompile(`\b(?:\+?1[-. ]?)?\(?([0-9]{3})\)?[-. ]?([0-9]{3})[-. ]?([0-9]{4})\b`),
		Mask:    "(***) ***-****",
	},
	{
		Name:    "IBAN",
		Pattern: regexp.MustCompile(`\b[A-Z]{2}[0-9]{2}[A-Z0-9]{11,30}\b`),
		Mask:    "**** **** **** ****",
	},
	{
		Name:    "Passport",
		Pattern: regexp.MustCompile(`\b[A-Z0-9]{6,9}\b`),
		Mask:    "*********",
	},
}

type Engine struct {
	Scanners []Scanner
}

func NewEngine(scanners ...Scanner) *Engine {
	if len(scanners) == 0 {
		return &Engine{Scanners: DefaultScanners}
	}
	return &Engine{Scanners: scanners}
}

func (e *Engine) Mask(input string) string {
	res := input
	for _, s := range e.Scanners {
		res = s.Pattern.ReplaceAllString(res, s.Mask)
	}
	return res
}

func (e *Engine) Discover(input string) []string {
	var found []string
	for _, s := range e.Scanners {
		if s.Pattern.MatchString(input) {
			found = append(found, s.Name)
		}
	}
	return found
}

func MaskEmail(s string) string {
	parts := strings.Split(s, "@")
	if len(parts) == 2 {
		if len(parts[0]) > 1 {
			return parts[0][0:1] + "****@" + parts[1]
		}
		return "*@" + parts[1]
	}
	return "****"
}

func MaskPartial(s string) string {
	if len(s) > 4 {
		return s[:2] + "****" + s[len(s)-2:]
	}
	return "****"
}
