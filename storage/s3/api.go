package s3

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ObjectInfo is the metadata S3 reports for an object.
type ObjectInfo struct {
	Key             string
	Size            int64
	ETag            string
	ContentType     string
	ContentEncoding string
	LastModified    time.Time

	// Metadata holds user metadata, with the x-amz-meta- prefix removed.
	Metadata map[string]string
}

// Object is an object body together with its metadata. Body must be closed.
type Object struct {
	ObjectInfo
	Body io.ReadCloser
}

// PutResult reports what the endpoint stored.
type PutResult struct {
	ETag      string
	VersionID string
}

// PutOption configures a single Put.
type PutOption func(*putConfig)

type putConfig struct {
	contentType     string
	contentEncoding string
	contentLength   *int64
	metadata        map[string]string
}

// WithContentType sets the object's Content-Type.
func WithContentType(contentType string) PutOption {
	return func(c *putConfig) { c.contentType = contentType }
}

// WithContentEncoding sets the object's Content-Encoding, for a body that is
// already compressed.
func WithContentEncoding(encoding string) PutOption {
	return func(c *putConfig) { c.contentEncoding = encoding }
}

// WithContentLength states the body size for a stream the package cannot
// measure, which matters together with WithUnsignedPayload: without a length,
// the body goes out chunked, and AWS rejects a chunked PutObject.
func WithContentLength(length int64) PutOption {
	return func(c *putConfig) { c.contentLength = &length }
}

// WithMetadata attaches user metadata, sent as x-amz-meta-* headers.
func WithMetadata(metadata map[string]string) PutOption {
	return func(c *putConfig) { c.metadata = metadata }
}

// Put stores body under key.
//
// There is no multipart upload, so the whole object is sent in one request and
// the endpoint's single-request limit applies (5 GiB on AWS).
//
// SigV4 signs a hash of the body, so the body must be read twice. A body that
// implements io.Seeker (an *os.File, a *bytes.Reader) is hashed and rewound;
// anything else is buffered in memory. Configure WithUnsignedPayload to stream
// instead, at the cost of the signature no longer covering the body.
func (c *Client) Put(ctx context.Context, bucket, key string, body io.Reader, opts ...PutOption) (*PutResult, error) {
	cfg := &putConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	get, length, hash, err := c.bodyFromReader(body, cfg.contentLength)
	if err != nil {
		return nil, err
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

	resp, err := c.do(ctx, &request{
		op: "Put", method: http.MethodPut, bucket: bucket, key: key,
		header: header, body: get, length: length, payloadHash: hash,
	})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.errorFrom(&request{op: "Put", bucket: bucket, key: key}, resp)
	}
	drain(resp)

	return &PutResult{
		ETag:      resp.Header.Get("ETag"),
		VersionID: resp.Header.Get("X-Amz-Version-Id"),
	}, nil
}

// Get retrieves an object. The caller closes Object.Body.
func (c *Client) Get(ctx context.Context, bucket, key string) (*Object, error) {
	return c.get(ctx, bucket, key, nil)
}

// GetRange retrieves length bytes starting at offset. A length of zero or less
// reads to the end of the object.
func (c *Client) GetRange(ctx context.Context, bucket, key string, offset, length int64) (*Object, error) {
	spec := fmt.Sprintf("bytes=%d-", offset)
	if length > 0 {
		spec = fmt.Sprintf("bytes=%d-%d", offset, offset+length-1)
	}
	return c.get(ctx, bucket, key, map[string]string{"Range": spec})
}

func (c *Client) get(ctx context.Context, bucket, key string, header map[string]string) (*Object, error) {
	r := &request{
		op: "Get", method: http.MethodGet, bucket: bucket, key: key,
		header: header, payloadHash: emptyPayloadHash,
	}
	resp, err := c.do(ctx, r)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return nil, c.errorFrom(r, resp)
	}
	return &Object{ObjectInfo: objectInfo(key, resp.Header), Body: resp.Body}, nil
}

// Head reports an object's metadata without transferring it.
func (c *Client) Head(ctx context.Context, bucket, key string) (*ObjectInfo, error) {
	r := &request{
		op: "Head", method: http.MethodHead, bucket: bucket, key: key,
		payloadHash: emptyPayloadHash,
	}
	resp, err := c.do(ctx, r)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.errorFrom(r, resp)
	}
	drain(resp)
	info := objectInfo(key, resp.Header)
	return &info, nil
}

// Delete removes an object. Deleting a key that does not exist succeeds, which
// is how S3 itself behaves.
func (c *Client) Delete(ctx context.Context, bucket, key string) error {
	r := &request{
		op: "Delete", method: http.MethodDelete, bucket: bucket, key: key,
		payloadHash: emptyPayloadHash,
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

// ListOption configures a single List call.
type ListOption func(*listConfig)

type listConfig struct {
	prefix            string
	delimiter         string
	maxKeys           int
	continuationToken string
	startAfter        string
}

// WithPrefix limits the listing to keys with this prefix.
func WithPrefix(prefix string) ListOption {
	return func(c *listConfig) { c.prefix = prefix }
}

// WithDelimiter groups keys sharing a prefix up to the delimiter, reporting
// them in ListResult.CommonPrefixes. Use "/" to walk a listing like a
// directory tree.
func WithDelimiter(delimiter string) ListOption {
	return func(c *listConfig) { c.delimiter = delimiter }
}

// WithMaxKeys caps how many keys one page returns.
func WithMaxKeys(maxKeys int) ListOption {
	return func(c *listConfig) { c.maxKeys = maxKeys }
}

// WithContinuationToken resumes a truncated listing from ListResult.NextToken.
func WithContinuationToken(token string) ListOption {
	return func(c *listConfig) { c.continuationToken = token }
}

// WithStartAfter begins the listing after this key.
func WithStartAfter(key string) ListOption {
	return func(c *listConfig) { c.startAfter = key }
}

// ListResult is one page of a listing.
type ListResult struct {
	Objects        []ObjectInfo
	CommonPrefixes []string
	IsTruncated    bool

	// NextToken continues a truncated listing through WithContinuationToken.
	NextToken string
}

// listBucketResult is the ListObjectsV2 response document.
type listBucketResult struct {
	XMLName               xml.Name `xml:"ListBucketResult"`
	IsTruncated           bool     `xml:"IsTruncated"`
	NextContinuationToken string   `xml:"NextContinuationToken"`
	Contents              []struct {
		Key          string    `xml:"Key"`
		LastModified time.Time `xml:"LastModified"`
		ETag         string    `xml:"ETag"`
		Size         int64     `xml:"Size"`
	} `xml:"Contents"`
	CommonPrefixes []struct {
		Prefix string `xml:"Prefix"`
	} `xml:"CommonPrefixes"`
}

// maxListBody bounds a listing document.
const maxListBody = 16 << 20

// List returns one page of a bucket listing. A truncated page carries
// NextToken, which WithContinuationToken feeds back to fetch the next one.
func (c *Client) List(ctx context.Context, bucket string, opts ...ListOption) (*ListResult, error) {
	cfg := &listConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	params := [][2]string{{"list-type", "2"}}
	if cfg.prefix != "" {
		params = append(params, [2]string{"prefix", cfg.prefix})
	}
	if cfg.delimiter != "" {
		params = append(params, [2]string{"delimiter", cfg.delimiter})
	}
	if cfg.maxKeys > 0 {
		params = append(params, [2]string{"max-keys", strconv.Itoa(cfg.maxKeys)})
	}
	if cfg.continuationToken != "" {
		params = append(params, [2]string{"continuation-token", cfg.continuationToken})
	}
	if cfg.startAfter != "" {
		params = append(params, [2]string{"start-after", cfg.startAfter})
	}

	r := &request{
		op: "List", method: http.MethodGet, bucket: bucket,
		params: params, payloadHash: emptyPayloadHash,
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

	var doc listBucketResult
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, &Error{Op: "List", Bucket: bucket, StatusCode: resp.StatusCode,
			Code: "MalformedXML", Message: err.Error(), err: err}
	}

	result := &ListResult{IsTruncated: doc.IsTruncated, NextToken: doc.NextContinuationToken}
	for _, item := range doc.Contents {
		result.Objects = append(result.Objects, ObjectInfo{
			Key:          item.Key,
			Size:         item.Size,
			ETag:         item.ETag,
			LastModified: item.LastModified,
		})
	}
	for _, item := range doc.CommonPrefixes {
		result.CommonPrefixes = append(result.CommonPrefixes, item.Prefix)
	}
	return result, nil
}

// createBucketConfiguration is the body a non us-east-1 bucket creation needs.
type createBucketConfiguration struct {
	XMLName            xml.Name `xml:"CreateBucketConfiguration"`
	XMLNS              string   `xml:"xmlns,attr"`
	LocationConstraint string   `xml:"LocationConstraint"`
}

// CreateBucket creates a bucket in the client's region. A bucket that already
// belongs to the caller reports ErrBucketExists.
func (c *Client) CreateBucket(ctx context.Context, bucket string) error {
	r := &request{
		op: "CreateBucket", method: http.MethodPut, bucket: bucket,
		payloadHash: emptyPayloadHash,
	}
	// us-east-1 is the default location and must not be stated explicitly.
	region := c.Region()
	if region != "us-east-1" {
		body, err := xml.Marshal(&createBucketConfiguration{
			XMLNS:              "http://s3.amazonaws.com/doc/2006-03-01/",
			LocationConstraint: region,
		})
		if err != nil {
			return err
		}
		get, length, hash, err := c.bodyFromReader(bytes.NewReader(body), nil)
		if err != nil {
			return err
		}
		r.body, r.length, r.payloadHash = get, length, hash
		r.header = map[string]string{"Content-Type": "application/xml"}
	}

	resp, err := c.do(ctx, r)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return c.errorFrom(r, resp)
	}
	drain(resp)
	return nil
}

// DeleteBucket removes an empty bucket.
func (c *Client) DeleteBucket(ctx context.Context, bucket string) error {
	r := &request{
		op: "DeleteBucket", method: http.MethodDelete, bucket: bucket,
		payloadHash: emptyPayloadHash,
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

// objectInfo reads the metadata headers common to GET and HEAD.
func objectInfo(key string, header http.Header) ObjectInfo {
	info := ObjectInfo{
		Key:             key,
		Size:            -1,
		ETag:            header.Get("ETag"),
		ContentType:     header.Get("Content-Type"),
		ContentEncoding: header.Get("Content-Encoding"),
	}
	if v := header.Get("Content-Length"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			info.Size = n
		}
	}
	// A ranged response reports the object size, not the slice size.
	if v := header.Get("Content-Range"); v != "" {
		if i := strings.LastIndex(v, "/"); i != -1 {
			if n, err := strconv.ParseInt(v[i+1:], 10, 64); err == nil {
				info.Size = n
			}
		}
	}
	if v := header.Get("Last-Modified"); v != "" {
		if t, err := http.ParseTime(v); err == nil {
			info.LastModified = t
		}
	}
	for name, values := range header {
		const prefix = "X-Amz-Meta-"
		if len(values) > 0 && strings.HasPrefix(http.CanonicalHeaderKey(name), prefix) {
			if info.Metadata == nil {
				info.Metadata = map[string]string{}
			}
			info.Metadata[strings.ToLower(http.CanonicalHeaderKey(name)[len(prefix):])] = values[0]
		}
	}
	return info
}
