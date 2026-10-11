package firewall

import (
	"net/url"
	"strings"
	"unicode"
)

/*
Transformation pipeline — the part of a WAF that actually decides whether it
can be evaded.

A payload can be written many ways: percent-encoded twice, split by SQL
comments, padded with null bytes, spelled with HTML entities, or disguised with
Unicode homoglyphs. Signatures that match raw input catch only the naive
version. So, exactly like ModSecurity/CRS, every target is pushed through a
canonicalisation chain BEFORE any rule runs, and rules are written against the
canonical form.

Deliberately ordered: decode first (so encoded comments become comments), then
strip structure, then fold case/whitespace last.
*/

// maxNormalizeLen caps work per target so a huge body can't burn CPU.
const maxNormalizeLen = 128 * 1024

// normalize canonicalises one target value for signature matching.
func normalize(s string) string {
	if len(s) > maxNormalizeLen {
		s = s[:maxNormalizeLen]
	}

	s = decodeRepeatedly(s)     // %2527 → %27 → '
	s = decodeHTMLEntities(s)   // &#x27; &#39; &apos; → '
	s = stripNullBytes(s)       // %00 padding
	s = foldUnicodeLookalikes(s)// fullwidth / homoglyph → ascii
	s = stripSQLComments(s)     // UNI/**/ON → UNION
	s = collapseWhitespace(s)   // tabs, newlines, repeated spaces → one space
	return strings.ToLower(s)
}

// decodeRepeatedly URL-decodes until stable (defeats double/triple encoding).
// Also converts '+' to space, which form encoding uses.
func decodeRepeatedly(s string) string {
	cur := s
	for i := 0; i < 4; i++ { // bounded: attackers can nest, we can't loop forever
		dec, err := url.QueryUnescape(cur)
		if err != nil {
			// tolerate bad escapes — decode what we can, char by char
			dec = lenientUnescape(cur)
		}
		if dec == cur {
			break
		}
		cur = dec
	}
	return cur
}

// lenientUnescape decodes %XX sequences and ignores malformed ones, so a
// deliberately broken escape can't stop the whole decode.
func lenientUnescape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			hi, ok1 := unhex(s[i+1])
			lo, ok2 := unhex(s[i+2])
			if ok1 && ok2 {
				b.WriteByte(hi<<4 | lo)
				i += 2
				continue
			}
		}
		if s[i] == '+' {
			b.WriteByte(' ')
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// htmlEntities covers the characters that matter for injection payloads.
var htmlEntities = strings.NewReplacer(
	"&lt;", "<", "&LT;", "<", "&#60;", "<", "&#x3c;", "<", "&#X3C;", "<",
	"&gt;", ">", "&GT;", ">", "&#62;", ">", "&#x3e;", ">", "&#X3E;", ">",
	"&quot;", "\"", "&#34;", "\"", "&#x22;", "\"",
	"&apos;", "'", "&#39;", "'", "&#x27;", "'", "&#X27;", "'",
	"&amp;", "&", "&#38;", "&", "&#x26;", "&",
	"&#40;", "(", "&#x28;", "(", "&#41;", ")", "&#x29;", ")",
	"&colon;", ":", "&#58;", ":", "&#x3a;", ":",
	"&sol;", "/", "&#47;", "/", "&#x2f;", "/",
	"&NewLine;", "\n", "&Tab;", "\t",
)

func decodeHTMLEntities(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	prev := ""
	for i := 0; i < 2 && s != prev; i++ { // entities can be nested once
		prev = s
		s = htmlEntities.Replace(s)
	}
	return s
}

// stripNullBytes removes NULs and other control chars used to truncate parsers
// mid-payload (a classic way to slip past naive matching).
func stripNullBytes(s string) string {
	if !strings.ContainsAny(s, "\x00\x0b\x0c") {
		return s
	}
	return strings.Map(func(r rune) rune {
		switch r {
		case 0, '\v', '\f':
			return -1
		}
		return r
	}, s)
}

// stripSQLComments removes /*...*/ (including the MySQL /*!...*/ form) and
// collapses what's left, so comment-split keywords rejoin into one token.
func stripSQLComments(s string) string {
	if !strings.Contains(s, "/*") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	depth := 0
	for i := 0; i < len(s); i++ {
		if i+1 < len(s) && s[i] == '/' && s[i+1] == '*' {
			depth++
			i++
			continue
		}
		if depth > 0 && i+1 < len(s) && s[i] == '*' && s[i+1] == '/' {
			depth--
			i++
			continue
		}
		if depth == 0 {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// foldUnicodeLookalikes maps fullwidth and common homoglyph characters back to
// their ASCII equivalents, so a payload written in fullwidth Latin still
// matches an ASCII signature.
func foldUnicodeLookalikes(s string) string {
	ascii := true
	for _, r := range s {
		if r > unicode.MaxASCII {
			ascii = false
			break
		}
	}
	if ascii {
		return s
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 0xFF01 && r <= 0xFF5E: // fullwidth ASCII block
			return r - 0xFEE0
		case r == 0x2018 || r == 0x2019 || r == 0x02B9 || r == 0x02BC: // curly / modifier apostrophes
			return '\''
		case r == 0x201C || r == 0x201D:
			return '"'
		case r == 0x2215 || r == 0x2044: // division / fraction slash
			return '/'
		case r == 0x00A0 || (r >= 0x2000 && r <= 0x200B) || r == 0x3000: // exotic spaces
			return ' '
		case r == 0x200C || r == 0x200D || r == 0xFEFF: // zero-width joiners / BOM
			return -1
		}
		return r
	}, s)
}

// collapseWhitespace turns every run of whitespace into a single space, so
// keyword padding (tabs, newlines, repeated spaces) doesn't break matching.
func collapseWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !space {
				b.WriteByte(' ')
				space = true
			}
			continue
		}
		space = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}
