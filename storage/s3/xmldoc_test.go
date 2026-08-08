package s3

import (
	"strings"
	"testing"
)

func TestParseListBucketResultSubset(t *testing.T) {
	// Exercises the corners the scanner has to survive: prolog, comments,
	// namespace prefixes, attributes with '>' inside quotes, self-closing
	// optional elements, unknown elements with nesting, and entities.
	const doc = `<?xml version="1.0" encoding="UTF-8"?>
<!-- comment > with a bracket -->
<s3:ListBucketResult xmlns:s3="http://s3.amazonaws.com/doc/2006-03-01/" note="a > b">
  <IsTruncated>false</IsTruncated>
  <NextContinuationToken/>
  <Unknown><Nested><Deep>x</Deep></Nested></Unknown>
  <Contents>
    <Key>a&amp;b &lt;c&gt; &#65;&#x42; caf&#xE9;</Key>
    <LastModified>2024-02-29T01:02:03Z</LastModified>
    <ETag>&quot;e&quot;</ETag>
    <Size>7</Size>
    <StorageClass>STANDARD</StorageClass>
  </Contents>
  <CommonPrefixes><Prefix>p/</Prefix></CommonPrefixes>
</s3:ListBucketResult>`

	result, err := parseListBucketResult([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsTruncated || result.NextToken != "" {
		t.Errorf("IsTruncated = %v, NextToken = %q", result.IsTruncated, result.NextToken)
	}
	if len(result.Objects) != 1 {
		t.Fatalf("Objects = %+v", result.Objects)
	}
	obj := result.Objects[0]
	if obj.Key != "a&b <c> AB café" {
		t.Errorf("Key = %q", obj.Key)
	}
	if obj.ETag != `"e"` || obj.Size != 7 {
		t.Errorf("ETag = %q, Size = %d", obj.ETag, obj.Size)
	}
	if obj.LastModified.Day() != 29 {
		t.Errorf("LastModified = %v", obj.LastModified)
	}
	if len(result.CommonPrefixes) != 1 || result.CommonPrefixes[0] != "p/" {
		t.Errorf("CommonPrefixes = %v", result.CommonPrefixes)
	}
}

func TestParseListBucketResultMalformed(t *testing.T) {
	for name, doc := range map[string]string{
		"empty":         "",
		"wrong root":    `<Wrong></Wrong>`,
		"truncated":     `<ListBucketResult><Contents><Key>k</Key>`,
		"mismatch":      `<ListBucketResult><Contents></Wrong></ListBucketResult>`,
		"bad size":      `<ListBucketResult><Contents><Size>x</Size></Contents></ListBucketResult>`,
		"bad truncated": `<ListBucketResult><IsTruncated>maybe</IsTruncated></ListBucketResult>`,
		"bad date":      `<ListBucketResult><Contents><LastModified>yesterday</LastModified></Contents></ListBucketResult>`,
	} {
		if _, err := parseListBucketResult([]byte(doc)); err == nil {
			t.Errorf("%s: parsed without error", name)
		}
	}
}

func TestParseErrorDocument(t *testing.T) {
	const doc = `<?xml version="1.0"?><Error>
  <Code>NoSuchKey</Code>
  <Message>The specified key does not exist.</Message>
  <RequestId>req-1</RequestId>
  <Extra>ignored</Extra>
</Error>`
	code, message, requestID, ok := parseErrorDocument([]byte(doc))
	if !ok || code != "NoSuchKey" || requestID != "req-1" ||
		!strings.Contains(message, "does not exist") {
		t.Errorf("got %q %q %q ok=%v", code, message, requestID, ok)
	}

	for name, bad := range map[string]string{
		"empty":     "",
		"not xml":   "plain text body",
		"html":      "<html><body>gateway error</body></html>",
		"truncated": "<Error><Code>AccessDenied</Code>",
	} {
		if _, _, _, ok := parseErrorDocument([]byte(bad)); ok {
			t.Errorf("%s: accepted as an error document", name)
		}
	}
}

func TestXMLEscapeText(t *testing.T) {
	if got := xmlEscapeText("eu-<west>&1"); got != "eu-&lt;west&gt;&amp;1" {
		t.Errorf("escaped = %q", got)
	}
	if got := xmlEscapeText("us-east-1"); got != "us-east-1" {
		t.Errorf("escaped = %q", got)
	}
}
