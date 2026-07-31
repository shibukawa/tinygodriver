package aws

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestSignS3Example checks the signature against the GET Object example in the
// SigV4 documentation. The expected value was reproduced with aws-sdk-go-v2
// aws/signer/v4 over the same request.
func TestSignS3Example(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=0-9")

	creds := Credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
	}
	Sign(req, creds, SignRequest{
		Service:     "s3",
		Region:      "us-east-1",
		PayloadHash: EmptyPayloadHash,
		Time:        time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC),
	})

	const want = "AWS4-HMAC-SHA256 " +
		"Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, " +
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, " +
		"Signature=67fe34c8530db585abddc51067328adfedb6e42487d2566dc7d927d6e2722900"
	if got := req.Header.Get("Authorization"); got != want {
		t.Errorf("Authorization =\n %s\nwant\n %s", got, want)
	}
	if got := req.Header.Get("X-Amz-Date"); got != "20130524T000000Z" {
		t.Errorf("X-Amz-Date = %q", got)
	}
}

// The DynamoDB fixtures below were produced by aws-sdk-go-v2 aws/signer/v4
// signing the same request with the same header set, which is what makes them
// evidence about the service name reaching both the scope and the signing key
// rather than a restatement of this implementation.
const (
	ddbBody = `{"TableName":"users","Key":{"pk":{"S":"u#1"}}}`
	ddbHash = "b46a2d0f552d0c9cd57a0fee3a8a896728b45e5ead8f7ebcd666ac914bcfa645"

	ddbWant = "AWS4-HMAC-SHA256 " +
		"Credential=AKIAIOSFODNN7EXAMPLE/20260731/ap-northeast-1/dynamodb/aws4_request, " +
		"SignedHeaders=content-type;host;x-amz-content-sha256;x-amz-date;x-amz-target, " +
		"Signature=d49bc356c32d8946b283c7b22540548ecaf9973846b08e2eea82e49661478bda"

	ddbWantWithToken = "AWS4-HMAC-SHA256 " +
		"Credential=AKIAIOSFODNN7EXAMPLE/20260731/ap-northeast-1/dynamodb/aws4_request, " +
		"SignedHeaders=content-type;host;x-amz-content-sha256;x-amz-date;x-amz-security-token;x-amz-target, " +
		"Signature=763c8eedbc6541404796687a01cc26069447a2b9880e4e461a15c650425352a2"
)

var ddbTime = time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)

func ddbRequest(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost,
		"https://dynamodb.ap-northeast-1.amazonaws.com/", strings.NewReader(ddbBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "DynamoDB_20120810.GetItem")
	return req
}

func TestSignDynamoDBExample(t *testing.T) {
	req := ddbRequest(t)
	if got := SHA256Hex([]byte(ddbBody)); got != ddbHash {
		t.Fatalf("payload hash = %q, want %q", got, ddbHash)
	}

	Sign(req, Credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
	}, SignRequest{
		Service:     "dynamodb",
		Region:      "ap-northeast-1",
		PayloadHash: ddbHash,
		Time:        ddbTime,
	})

	if got := req.Header.Get("Authorization"); got != ddbWant {
		t.Errorf("Authorization =\n %s\nwant\n %s", got, ddbWant)
	}
}

func TestSignDynamoDBWithSessionToken(t *testing.T) {
	req := ddbRequest(t)
	Sign(req, Credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		SessionToken:    "session-token",
	}, SignRequest{
		Service:     "dynamodb",
		Region:      "ap-northeast-1",
		PayloadHash: ddbHash,
		Time:        ddbTime,
	})

	if got := req.Header.Get("X-Amz-Security-Token"); got != "session-token" {
		t.Errorf("X-Amz-Security-Token = %q", got)
	}
	if got := req.Header.Get("Authorization"); got != ddbWantWithToken {
		t.Errorf("Authorization =\n %s\nwant\n %s", got, ddbWantWithToken)
	}
}

// TestSignServiceReachesScopeAndKey is the property rule
// sigv4-service-parameterization exists for: changing only the service must
// change the signature, not just the scope string that is printed.
func TestSignServiceReachesScopeAndKey(t *testing.T) {
	creds := Credentials{AccessKeyID: "id", SecretAccessKey: "secret"}
	sr := SignRequest{Region: "us-east-1", PayloadHash: EmptyPayloadHash, Time: ddbTime}

	sigs := map[string]string{}
	for _, service := range []string{"s3", "dynamodb"} {
		req, err := http.NewRequest(http.MethodGet, "https://example.amazonaws.com/", nil)
		if err != nil {
			t.Fatal(err)
		}
		sr.Service = service
		Sign(req, creds, sr)
		auth := req.Header.Get("Authorization")
		if !strings.Contains(auth, "/"+service+"/aws4_request") {
			t.Errorf("%s: scope missing the service: %s", service, auth)
		}
		sigs[service] = auth[strings.Index(auth, "Signature="):]
	}
	if sigs["s3"] == sigs["dynamodb"] {
		t.Error("signature is identical across services, so the service does not reach the signing key")
	}
}

func TestSignDoubleEncodePath(t *testing.T) {
	sign := func(double bool) string {
		req, err := http.NewRequest(http.MethodGet, "https://example.amazonaws.com/", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.URL.Path = "/a b"
		req.URL.RawPath = URIEncode("/a b", false)
		Sign(req, Credentials{AccessKeyID: "id", SecretAccessKey: "secret"}, SignRequest{
			Service: "svc", Region: "us-east-1", PayloadHash: EmptyPayloadHash,
			DoubleEncodePath: double, Time: ddbTime,
		})
		return req.Header.Get("Authorization")
	}
	if sign(false) == sign(true) {
		t.Error("DoubleEncodePath did not change the signature, so it is not reaching the canonical request")
	}
}

// TestSignHonoursRequestHost covers a request whose Host was overridden: the
// signature covers the host header, so signing URL.Host there would sign a
// request nobody sends.
func TestSignHonoursRequestHost(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://127.0.0.1:8000/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "dynamodb.ap-northeast-1.amazonaws.com"
	Sign(req, Credentials{AccessKeyID: "id", SecretAccessKey: "secret"}, SignRequest{
		Service: "dynamodb", Region: "ap-northeast-1", PayloadHash: EmptyPayloadHash, Time: ddbTime,
	})

	other, err := http.NewRequest(http.MethodGet, "https://dynamodb.ap-northeast-1.amazonaws.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	Sign(other, Credentials{AccessKeyID: "id", SecretAccessKey: "secret"}, SignRequest{
		Service: "dynamodb", Region: "ap-northeast-1", PayloadHash: EmptyPayloadHash, Time: ddbTime,
	})
	if req.Header.Get("Authorization") != other.Header.Get("Authorization") {
		t.Error("Request.Host was not the host that got signed")
	}
}

func TestURIEncode(t *testing.T) {
	for _, test := range []struct {
		name        string
		in          string
		encodeSlash bool
		want        string
	}{
		{"unreserved", "abcXYZ019-_.~", true, "abcXYZ019-_.~"},
		{"path keeps slash", "/a/b c", false, "/a/b%20c"},
		{"query escapes slash", "a/b", true, "a%2Fb"},
		{"parens are escaped", "2019 (a)", true, "2019%20%28a%29"},
		{"utf-8", "こん", true, "%E3%81%93%E3%82%93"},
		{"plus stays literal", "a+b", true, "a%2Bb"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := URIEncode(test.in, test.encodeSlash); got != test.want {
				t.Errorf("URIEncode(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}

func TestCanonicalQuery(t *testing.T) {
	got := CanonicalQuery([][2]string{
		{"prefix", "photos/2019 (a)/"},
		{"list-type", "2"},
		{"uploads", ""},
	})
	const want = "list-type=2&prefix=photos%2F2019%20%28a%29%2F&uploads="
	if got != want {
		t.Errorf("CanonicalQuery = %q, want %q", got, want)
	}
}

func TestEndpointFromEnv(t *testing.T) {
	t.Setenv("AWS_ENDPOINT_URL", "http://generic:1")
	t.Setenv("AWS_ENDPOINT_URL_DYNAMODB", "http://ddb:2")

	if got := EndpointFromEnv("dynamodb"); got != "http://ddb:2" {
		t.Errorf("EndpointFromEnv(dynamodb) = %q", got)
	}
	if got := EndpointFromEnv("s3"); got != "http://generic:1" {
		t.Errorf("EndpointFromEnv(s3) = %q, want the generic fallback", got)
	}
	if got := EndpointFromEnv(""); got != "http://generic:1" {
		t.Errorf("EndpointFromEnv(\"\") = %q", got)
	}
}

func TestCredentialsFromEnv(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "id")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	t.Setenv("AWS_SESSION_TOKEN", "token")

	creds := CredentialsFromEnv()
	if !creds.Valid() || creds.SessionToken != "token" {
		t.Errorf("CredentialsFromEnv = %+v", creds)
	}

	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	if CredentialsFromEnv().Valid() {
		t.Error("credentials without a secret reported valid")
	}
}
