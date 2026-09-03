package s3

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Presign expiry bounds. S3 rejects X-Amz-Expires above seven days; the
// default is the one the AWS SDKs use.
const (
	DefaultPresignExpiry = 15 * time.Minute
	MaxPresignExpiry     = 7 * 24 * time.Hour
)

// PresignOptions describe the request a presigned URL authorizes.
type PresignOptions struct {
	// Method is the HTTP method the URL authorizes: GET, PUT, HEAD or
	// DELETE. Empty means GET.
	Method string

	// Expires bounds the URL's life, written as X-Amz-Expires in whole seconds
	// and rounded up. Zero means DefaultPresignExpiry; more than
	// MaxPresignExpiry, which S3 would reject, is ErrPresignExpiry.
	Expires time.Duration

	// ContentType, when set, is signed, so the sender must send exactly it.
	ContentType string

	// Headers are further request headers to sign. Each is a header the
	// sender must reproduce exactly, so a PUT that stores Content-Disposition
	// or x-amz-meta-* metadata names them here, and a plain GET names none: a
	// link in a page cannot add a header.
	Headers map[string]string

	// Query adds parameters to the URL, signed with it. On a GET,
	// response-content-disposition and response-content-type shape the reply
	// without asking the browser for a header; on a part upload, uploadId and
	// partNumber address the part.
	Query map[string]string
}

// Presign returns a URL that authorizes one request against key without the
// caller's credentials, signed through SigV4 query parameters. The endpoint,
// region, addressing style and credentials are the ones Get and Put use, so a
// URL for an S3-compatible server needs no second configuration.
//
// The body is not covered: the signature carries UNSIGNED-PAYLOAD, because
// whoever sends the request never sees the credentials and the signer never
// sees the body. Only the host and the headers named in opts are signed.
//
// Presigning is a pure function of the client and its arguments; ctx is here
// so the signature matches the rest of the client, and it goes unused.
func (c *Client) Presign(ctx context.Context, bucket, key string, opts PresignOptions) (*url.URL, error) {
	_ = ctx
	method := opts.Method
	if method == "" {
		method = http.MethodGet
	}
	expires := opts.Expires
	if expires == 0 {
		expires = DefaultPresignExpiry
	}
	if expires < 0 || expires > MaxPresignExpiry {
		return nil, ErrPresignExpiry
	}

	var params [][2]string
	if len(opts.Query) > 0 {
		params = make([][2]string, 0, len(opts.Query))
		for name, value := range opts.Query {
			params = append(params, [2]string{name, value})
		}
	}
	target := c.buildURL(&request{bucket: bucket, key: key, params: params})

	req := &http.Request{
		Method: method,
		URL:    target,
		Host:   target.Host,
		Header: make(http.Header, len(opts.Headers)+1),
	}
	if opts.ContentType != "" {
		req.Header.Set("Content-Type", opts.ContentType)
	}
	for name, value := range opts.Headers {
		req.Header.Set(name, value)
	}
	presign(req, c.creds, c.Region(), expires, c.now())
	return target, nil
}
