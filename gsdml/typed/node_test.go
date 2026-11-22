// SPDX-License-Identifier: MIT

package typed_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	profinet "github.com/otfabric/go-profinet"
	"github.com/otfabric/go-profinet/gsdml/typed"
)

func TestParseNode(t *testing.T) {
	n, err := typed.ParseNode([]byte("\xEF\xBB\xBF" + `<?xml version="1.0"?>
<!DOCTYPE a SYSTEM "a.dtd">
<a xmlns="urn:a" xmlns:p="urn:p" p:x="1" y="2" xml:lang="en"><!-- c --><p:b>t<![CDATA[<u>]]></p:b><c xmlns="">v</c><?pi x?></a>`))
	if err != nil {
		t.Fatal(err)
	}
	if n.Name.Space != "urn:a" || n.Line != 3 || len(n.Elements()) != 2 {
		t.Fatalf("root %+v", n)
	}
	if v, ok := n.Attr("urn:p", "x"); !ok || v != "1" {
		t.Error("prefixed attribute")
	}
	if _, ok := n.Attr("", "y"); !ok {
		t.Error("unprefixed attribute has no namespace")
	}
	if _, ok := n.Attr("http://www.w3.org/XML/1998/namespace", "lang"); !ok {
		t.Error("xml:lang")
	}
	b := n.Elements()[0]
	if b.Name.Space != "urn:p" || b.CharData() != "t<u>" || !b.Children[0].IsText() {
		t.Errorf("b %+v", b)
	}
	if c := n.Elements()[1]; c.Name.Space != "" || c.CharData() != "v" {
		t.Errorf("c %+v", c)
	}
	out := n.Marshal()
	n2, err := typed.ParseNode(out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !bytes.Equal(n2.Marshal(), out) {
		t.Error("Marshal is not stable")
	}
	if _, err := typed.ParseNode([]byte(`<?xml version="1.0" encoding="ISO-8859-1"?><a>` + "\xE9" + `</a>`)); err != nil {
		t.Errorf("ISO-8859-1: %v", err)
	}
}

func TestParseNodeErrors(t *testing.T) {
	cases := map[string]struct {
		doc  string
		want error
	}{
		"empty":                {``, profinet.ErrMalformed},
		"no root":              {`<?xml version="1.0"?><!-- -->`, profinet.ErrMalformed},
		"two roots":            {`<a/><b/>`, profinet.ErrMalformed},
		"unclosed":             {`<a><b></b>`, profinet.ErrMalformed},
		"mismatched":           {`<a></b>`, profinet.ErrMalformed},
		"mismatched prefix":    {`<p:a xmlns:p="urn:p" xmlns:q="urn:p"></q:a>`, profinet.ErrMalformed},
		"undeclared prefix":    {`<a><p:b/></a>`, profinet.ErrMalformed},
		"undeclared attribute": {`<a p:x="1"/>`, profinet.ErrMalformed},
		"undeclaring":          {`<a xmlns:p=""/>`, profinet.ErrMalformed},
		"xml rebound":          {`<a xmlns:xml="urn:x"/>`, profinet.ErrMalformed},
		"xml namespace bound":  {`<a xmlns:p="http://www.w3.org/XML/1998/namespace"/>`, profinet.ErrMalformed},
		"xmlns prefix":         {`<a xmlns:xmlns="urn:x"/>`, profinet.ErrMalformed},
		"xmlns namespace":      {`<a xmlns="http://www.w3.org/2000/xmlns/"/>`, profinet.ErrMalformed},
		"xmlns element":        {`<xmlns:a/>`, profinet.ErrMalformed},
		"duplicate expanded":   {`<a xmlns:p="urn:x" xmlns:q="urn:x" p:y="1" q:y="2"/>`, profinet.ErrMalformed},
		"internal subset":      {`<!DOCTYPE a [<!ENTITY e "x">]><a>&e;</a>`, profinet.ErrUnsupported},
		"encoding":             {`<?xml version="1.0" encoding="UTF-16"?><a/>`, profinet.ErrMalformed},
		"entity":               {`<a>&nope;</a>`, profinet.ErrMalformed},
	}
	for name, c := range cases {
		if _, err := typed.ParseNode([]byte(c.doc)); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	// a DOCTYPE with "[" in a quoted system ID has no internal subset
	if _, err := typed.ParseNode([]byte(`<!DOCTYPE a SYSTEM "a[1].dtd"><a/>`)); err != nil {
		t.Errorf("quoted [: %v", err)
	}
}

func TestParseNodeLimits(t *testing.T) {
	deep := strings.Repeat("<a>", typed.MaxDepth+1) + strings.Repeat("</a>", typed.MaxDepth+1)
	if _, err := typed.ParseNode([]byte(deep)); !errors.Is(err, profinet.ErrResourceExhausted) {
		t.Errorf("depth: %v", err)
	}
	if _, err := typed.ParseNode(make([]byte, typed.MaxDocumentSize+1)); !errors.Is(err, profinet.ErrResourceExhausted) {
		t.Errorf("size: %v", err)
	}
	if testing.Short() {
		return
	}
	many := "<a>" + strings.Repeat("<b/>", typed.MaxElements) + "</a>"
	if _, err := typed.ParseNode([]byte(many)); !errors.Is(err, profinet.ErrResourceExhausted) {
		t.Errorf("elements: %v", err)
	}
}
