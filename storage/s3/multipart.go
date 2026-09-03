package s3

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Part number bounds, which S3 fixes. A part below the 5 MiB minimum is
// refused by the endpoint at completion, not here: the last part may be
// smaller, and only the endpoint knows which one that is.
const (
	MinPartNumber = 1
	MaxPartNumber = 10000
)

// MultipartUpload identifies an upload in progress. CreateMultipartUpload
// returns one, and the other multipart calls take it back, so the bucket, key
// and upload ID cannot drift apart between calls.
type MultipartUpload struct {
	Bucket   string
	Key      string
	UploadID string
}

// CompletedPart is what UploadPart reports and CompleteMultipartUpload needs:
// the part's number and the ETag the endpoint assigned it.
type CompletedPart struct {
	PartNumber int
	ETag       string
}

// CreateMultipartUpload starts an upload of key in parts. The Put options set
// the object's Content-Type, Content-Encoding and metadata; they are fixed
// here, since the parts carry only bytes.
//
// An upload that is neither completed nor aborted keeps its parts, and AWS
// bills them, until a lifecycle rule removes it. Abort on every failure path.
func (c *Client) CreateMultipartUpload(ctx context.Context, bucket, key string, opts ...PutOption) (*MultipartUpload, error) {
	cfg := &putConfig{}
	for _, opt := range opts {
		opt(cfg)
	}
	header := map[string]string{}
	if cfg.contentType != "" {
		header["Content-Type"] = cfg.contentType
	}
	if cfg.contentEncoding != "" {
		header["Content-Encoding"] = cfg.contentEncoding
	}
	for name, value := range cfg.metadata {
		header["X-Amz-Meta-"+name] = value
	}

	r := &request{
		op: "CreateMultipartUpload", method: http.MethodPost, bucket: bucket, key: key,
		params: [][2]string{{"uploads", ""}}, header: header, payloadHash: emptyPayloadHash,
	}
	resp, err := c.do(ctx, r)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.errorFrom(r, resp)
	}
	body, err := readBody(resp, maxListBody)
	if err != nil {
		return nil, err
	}
	upload := &MultipartUpload{Bucket: bucket, Key: key}
	err = parseFlatDocument(body, "InitiateMultipartUploadResult", func(name, text string) {
		if name == "UploadId" {
			upload.UploadID = text
		}
	})
	if err != nil || upload.UploadID == "" {
		return nil, &Error{Op: r.op, Bucket: bucket, Key: key, StatusCode: resp.StatusCode,
			Code: "MalformedXML", Message: "no UploadId in InitiateMultipartUploadResult", err: errMalformedXML}
	}
	return upload, nil
}

// UploadPart sends one part of upload. Part numbers run from MinPartNumber to
// MaxPartNumber and need not be contiguous or in order; the list handed to
// CompleteMultipartUpload decides the object's order. Sending a number twice
// replaces the earlier part.
//
// The body follows Put's rules: a seekable body is hashed and rewound,
// anything else is buffered, and WithUnsignedPayload streams. WithContentLength
// is the one Put option that applies here.
func (c *Client) UploadPart(ctx context.Context, upload MultipartUpload, partNumber int, body io.Reader, opts ...PutOption) (*CompletedPart, error) {
	if partNumber < MinPartNumber || partNumber > MaxPartNumber {
		return nil, &Error{Op: "UploadPart", Bucket: upload.Bucket, Key: upload.Key,
			Code: "InvalidPart", Message: "part number " + strconv.Itoa(partNumber) + " out of range", err: ErrInvalidPart}
	}
	cfg := &putConfig{}
	for _, opt := range opts {
		opt(cfg)
	}
	get, length, hash, err := c.bodyFromReader(body, cfg.contentLength)
	if err != nil {
		return nil, err
	}

	r := &request{
		op: "UploadPart", method: http.MethodPut, bucket: upload.Bucket, key: upload.Key,
		params: [][2]string{{"partNumber", strconv.Itoa(partNumber)}, {"uploadId", upload.UploadID}},
		body:   get, length: length, payloadHash: hash,
	}
	resp, err := c.do(ctx, r)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.errorFrom(&request{op: r.op, bucket: r.bucket, key: r.key}, resp)
	}
	drain(resp)
	return &CompletedPart{PartNumber: partNumber, ETag: resp.Header.Get("ETag")}, nil
}

// CompleteMultipartUpload assembles the parts, in the order given, into the
// object. parts must be in ascending part number order, which is what S3
// requires; each ETag is the one UploadPart reported.
//
// S3 may answer 200 and then carry an error document in the body when the
// assembly fails part way, and that reply is reported as an error here, as a
// 4xx would be.
func (c *Client) CompleteMultipartUpload(ctx context.Context, upload MultipartUpload, parts []CompletedPart) (*PutResult, error) {
	var doc bytes.Buffer
	doc.WriteString(`<CompleteMultipartUpload xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`)
	for _, p := range parts {
		doc.WriteString("<Part><PartNumber>")
		doc.WriteString(strconv.Itoa(p.PartNumber))
		doc.WriteString("</PartNumber><ETag>")
		doc.WriteString(xmlEscapeText(p.ETag))
		doc.WriteString("</ETag></Part>")
	}
	doc.WriteString("</CompleteMultipartUpload>")
	get, length, hash, err := c.bodyFromReader(bytes.NewReader(doc.Bytes()), nil)
	if err != nil {
		return nil, err
	}

	r := &request{
		op: "CompleteMultipartUpload", method: http.MethodPost, bucket: upload.Bucket, key: upload.Key,
		params: [][2]string{{"uploadId", upload.UploadID}},
		header: map[string]string{"Content-Type": "application/xml"},
		body:   get, length: length, payloadHash: hash,
	}
	resp, err := c.do(ctx, r)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.errorFrom(r, resp)
	}
	body, err := readBody(resp, maxListBody)
	if err != nil {
		return nil, err
	}
	if _, _, _, isError := parseErrorDocument(body); isError {
		return nil, newError(r.op, r.bucket, r.key, resp.StatusCode, body, resp.Header.Get("X-Amz-Request-Id"))
	}
	result := &PutResult{VersionID: resp.Header.Get("X-Amz-Version-Id")}
	err = parseFlatDocument(body, "CompleteMultipartUploadResult", func(name, text string) {
		if name == "ETag" {
			result.ETag = strings.TrimSpace(text)
		}
	})
	if err != nil {
		return nil, &Error{Op: r.op, Bucket: r.bucket, Key: r.key, StatusCode: resp.StatusCode,
			Code: "MalformedXML", Message: err.Error(), err: err}
	}
	return result, nil
}

// AbortMultipartUpload discards upload and every part it holds. Aborting an
// upload that no longer exists is ErrNoSuchUpload.
func (c *Client) AbortMultipartUpload(ctx context.Context, upload MultipartUpload) error {
	r := &request{
		op: "AbortMultipartUpload", method: http.MethodDelete, bucket: upload.Bucket, key: upload.Key,
		params: [][2]string{{"uploadId", upload.UploadID}}, payloadHash: emptyPayloadHash,
	}
	resp, err := c.do(ctx, r)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return c.errorFrom(r, resp)
	}
	drain(resp)
	return nil
}
