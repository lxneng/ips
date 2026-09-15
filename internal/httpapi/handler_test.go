package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"ips/internal/geo"
)

type locatorFunc func(netip.Addr) (geo.Location, error)

func (fn locatorFunc) Lookup(ip netip.Addr) (geo.Location, error) { return fn(ip) }

func quietLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

func TestLookup(t *testing.T) {
	location := geo.Location{Country: "中国", Province: "江苏省", City: "南京市", ISP: "电信", CountryCode: "CN"}
	for _, test := range []struct {
		name, input, canonical string
		version                int
	}{
		{"IPv4", "114.114.114.114", "114.114.114.114", 4},
		{"IPv6", "2001:4860:4860:0:0:0:0:8888", "2001:4860:4860::8888", 6},
		{"mapped IPv4", "::ffff:114.114.114.114", "114.114.114.114", 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			var received netip.Addr
			handler := New(locatorFunc(func(ip netip.Addr) (geo.Location, error) {
				received = ip
				return location, nil
			}), quietLogger(), func() bool { return true })
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/"+test.input, nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d; body = %s", recorder.Code, recorder.Body)
			}
			var response LookupResponse
			decoder := json.NewDecoder(strings.NewReader(recorder.Body.String()))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&response); err != nil {
				t.Fatal(err)
			}
			if response.IP != test.canonical || response.IPVersion != test.version || response.Location != location {
				t.Fatalf("unexpected response: %+v", response)
			}
			if received.String() != test.canonical {
				t.Fatalf("locator received %s", received)
			}
			if !strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") || recorder.Header().Get("X-Request-ID") == "" {
				t.Fatalf("missing response headers: %v", recorder.Header())
			}
			if got := recorder.Header().Get("Cache-Control"); got != ipCacheControl {
				t.Fatalf("Cache-Control = %q, want %q", got, ipCacheControl)
			}
		})
	}
}

func TestRootLooksUpRemoteAddress(t *testing.T) {
	location := geo.Location{Country: "中国", Province: "上海市", CountryCode: "CN"}
	for _, test := range []struct {
		name, remote, canonical string
		version                 int
	}{
		{"IPv4", "203.0.113.8:54321", "203.0.113.8", 4},
		{"IPv6", "[2001:db8::8]:54321", "2001:db8::8", 6},
		{"mapped IPv4", "[::ffff:203.0.113.8]:54321", "203.0.113.8", 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			var received netip.Addr
			handler := New(locatorFunc(func(ip netip.Addr) (geo.Location, error) {
				received = ip
				return location, nil
			}), quietLogger(), func() bool { return true })
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.RemoteAddr = test.remote
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d; body = %s", recorder.Code, recorder.Body)
			}
			var response LookupResponse
			decoder := json.NewDecoder(strings.NewReader(recorder.Body.String()))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&response); err != nil {
				t.Fatal(err)
			}
			if response.IP != test.canonical || response.IPVersion != test.version || response.Location != location || received.String() != test.canonical {
				t.Fatalf("response = %+v, remote = %s", response, received)
			}
			if got := recorder.Header().Get("Cache-Control"); got != noStoreCacheControl {
				t.Fatalf("Cache-Control = %q, want %q", got, noStoreCacheControl)
			}
		})
	}
}

func TestRootLooksUpForwardedAddress(t *testing.T) {
	for _, test := range []struct {
		name, forwarded, canonical string
		version                    int
	}{
		{"IPv4", "198.51.100.99", "198.51.100.99", 4},
		{"proxy chain", "198.51.100.99, 172.22.0.1", "198.51.100.99", 4},
		{"IPv6", " 2001:db8::8, 172.22.0.1 ", "2001:db8::8", 6},
		{"mapped IPv4", "::ffff:198.51.100.99", "198.51.100.99", 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			var received netip.Addr
			handler := New(locatorFunc(func(ip netip.Addr) (geo.Location, error) {
				received = ip
				return geo.Location{Country: "forwarded"}, nil
			}), quietLogger(), func() bool { return true })
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.RemoteAddr = "not-an-address"
			request.Header.Set("X-Forwarded-For", test.forwarded)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d; body = %s", recorder.Code, recorder.Body)
			}
			var response LookupResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.IP != test.canonical || response.IPVersion != test.version || received.String() != test.canonical {
				t.Fatalf("response = %+v, locator input = %s", response, received)
			}
		})
	}
}

func TestRootRejectsInvalidForwardedAddress(t *testing.T) {
	for _, forwarded := range []string{"invalid", " , 172.22.0.1", "1.2.3.4:80", "fe80::1%en0"} {
		t.Run(forwarded, func(t *testing.T) {
			handler := New(locatorFunc(func(netip.Addr) (geo.Location, error) {
				t.Fatal("invalid forwarded address reached the database")
				return geo.Location{}, nil
			}), quietLogger(), func() bool { return true })
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.RemoteAddr = "203.0.113.8:54321"
			request.Header.Set("X-Forwarded-For", forwarded)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			assertError(t, recorder, http.StatusBadRequest, "invalid_ip")
		})
	}
}

func TestRootRejectsInvalidRemoteAddress(t *testing.T) {
	handler := New(locatorFunc(func(netip.Addr) (geo.Location, error) {
		t.Fatal("invalid remote address reached the database")
		return geo.Location{}, nil
	}), quietLogger(), func() bool { return true })
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "not-an-address"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	assertError(t, recorder, http.StatusInternalServerError, "internal_error")
}

func TestBatchLookup(t *testing.T) {
	inputs := []string{"8.8.8.8", "2001:4860:4860:0:0:0:0:8888", "::ffff:8.8.8.8", "8.8.8.8"}
	wantIPs := []string{"8.8.8.8", "2001:4860:4860::8888", "8.8.8.8", "8.8.8.8"}
	wantVersions := []int{4, 6, 4, 4}
	var received []string
	handler := New(locatorFunc(func(ip netip.Addr) (geo.Location, error) {
		received = append(received, ip.String())
		return geo.Location{Country: ip.String(), CountryCode: "ZZ"}, nil
	}), quietLogger(), func() bool { return true })
	body, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", recorder.Code, recorder.Body)
	}
	var response BatchLookupResponse
	decoder := json.NewDecoder(strings.NewReader(recorder.Body.String()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "success" || len(response.Successes) != len(wantIPs) || len(response.Failures) != 0 {
		t.Fatalf("unexpected batch response: %+v", response)
	}
	for i := range response.Successes {
		if response.Successes[i].IP != wantIPs[i] || response.Successes[i].IPVersion != wantVersions[i] || response.Successes[i].Country != wantIPs[i] || response.Successes[i].CountryCode != "ZZ" {
			t.Fatalf("successes[%d] = %+v", i, response.Successes[i])
		}
		if received[i] != wantIPs[i] {
			t.Fatalf("locator input[%d] = %q, want %q", i, received[i], wantIPs[i])
		}
	}
	if got := recorder.Header().Get("Cache-Control"); got != noStoreCacheControl {
		t.Fatalf("Cache-Control = %q, want %q", got, noStoreCacheControl)
	}
}

func TestBatchRequestValidation(t *testing.T) {
	tooMany, err := json.Marshal(make([]string, maxBatchIPs+1))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, contentType, body, code string
		status                        int
	}{
		{"missing content type", "", `["8.8.8.8"]`, "unsupported_media_type", http.StatusUnsupportedMediaType},
		{"wrong content type", "text/plain", `["8.8.8.8"]`, "unsupported_media_type", http.StatusUnsupportedMediaType},
		{"empty body", "application/json", "", "invalid_request", http.StatusBadRequest},
		{"malformed JSON", "application/json", `[`, "invalid_request", http.StatusBadRequest},
		{"wrong JSON type", "application/json", `{"ip":"8.8.8.8"}`, "invalid_request", http.StatusBadRequest},
		{"empty array", "application/json", `[]`, "invalid_request", http.StatusBadRequest},
		{"null", "application/json", `null`, "invalid_request", http.StatusBadRequest},
		{"trailing JSON", "application/json", `[] []`, "invalid_request", http.StatusBadRequest},
		{"non-string item", "application/json", `["8.8.8.8", 1]`, "invalid_request", http.StatusBadRequest},
		{"too many IPs", "application/json", string(tooMany), "batch_too_large", http.StatusRequestEntityTooLarge},
		{"body too large", "application/json", `[` + strings.Repeat(" ", maxBatchBodyBytes) + `]`, "request_too_large", http.StatusRequestEntityTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := New(locatorFunc(func(netip.Addr) (geo.Location, error) {
				t.Fatal("invalid batch reached the database")
				return geo.Location{}, nil
			}), quietLogger(), func() bool { return true })
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			assertError(t, recorder, test.status, test.code)
		})
	}
}

func TestBatchReturnsItemErrors(t *testing.T) {
	var received []string
	handler := New(locatorFunc(func(ip netip.Addr) (geo.Location, error) {
		received = append(received, ip.String())
		switch ip.String() {
		case "192.0.2.1":
			return geo.Location{}, geo.ErrNotFound
		case "1.1.1.1":
			return geo.Location{}, errors.New("secret internal filesystem path")
		default:
			return geo.Location{Country: "success"}, nil
		}
	}), quietLogger(), func() bool { return true })
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`["8.8.8.8", "invalid", "192.0.2.1", "1.1.1.1", "::ffff:8.8.8.8"]`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", recorder.Code, recorder.Body)
	}
	var response BatchLookupResponse
	decoder := json.NewDecoder(strings.NewReader(recorder.Body.String()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "partial_success" || len(response.Successes) != 2 || len(response.Failures) != 3 {
		t.Fatalf("unexpected batch response: %+v", response)
	}
	if response.Successes[0].IP != "8.8.8.8" || response.Successes[0].Country != "success" {
		t.Fatalf("unexpected success response: %+v", response.Successes[0])
	}
	for i, want := range []struct {
		ip, code string
	}{
		{"invalid", "invalid_ip"},
		{"192.0.2.1", "ip_not_found"},
		{"1.1.1.1", "internal_error"},
	} {
		item := response.Failures[i]
		if item.IP != want.ip || item.Error.Code != want.code || item.Error.Message == "" {
			t.Fatalf("failures[%d] = %+v, want IP %q and error %q", i, item, want.ip, want.code)
		}
	}
	if response.Successes[1].IP != "8.8.8.8" || response.Successes[1].IPVersion != 4 || response.Successes[1].Country != "success" {
		t.Fatalf("unexpected mapped IPv4 response: %+v", response.Successes[1])
	}
	if got := strings.Join(received, ","); got != "8.8.8.8,192.0.2.1,1.1.1.1,8.8.8.8" {
		t.Fatalf("locator inputs = %q", got)
	}
	if strings.Contains(recorder.Body.String(), "secret") {
		t.Fatal("internal details leaked to the client")
	}
	if got := recorder.Header().Get("Cache-Control"); got != noStoreCacheControl {
		t.Fatalf("Cache-Control = %q, want %q", got, noStoreCacheControl)
	}
}

func TestBatchReportsFailedWhenEveryItemFails(t *testing.T) {
	handler := New(locatorFunc(func(netip.Addr) (geo.Location, error) {
		t.Fatal("invalid IP reached the database")
		return geo.Location{}, nil
	}), quietLogger(), func() bool { return true })
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`["invalid", "also-invalid"]`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", recorder.Code, recorder.Body)
	}
	var response BatchLookupResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "failed" || len(response.Successes) != 0 || len(response.Failures) != 2 {
		t.Fatalf("unexpected batch response: %+v", response)
	}
}

func TestBatchRejectsWhenNotReady(t *testing.T) {
	handler := New(locatorFunc(func(netip.Addr) (geo.Location, error) {
		t.Fatal("lookup was called while unready")
		return geo.Location{}, nil
	}), quietLogger(), func() bool { return false })
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`["8.8.8.8"]`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	assertError(t, recorder, http.StatusServiceUnavailable, "not_ready")
}

func TestInvalidIPNeverReachesDatabase(t *testing.T) {
	handler := New(locatorFunc(func(netip.Addr) (geo.Location, error) {
		t.Fatal("invalid input reached the database")
		return geo.Location{}, nil
	}), quietLogger(), func() bool { return true })
	for _, input := range []string{"hello", "999.1.1.1", "1.2.3.4:80", "1.2.3.4%2F24", "fe80::1%25en0", "%20", "[::1]"} {
		t.Run(input, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/"+input, nil))
			assertError(t, recorder, http.StatusBadRequest, "invalid_ip")
		})
	}
}

func TestLookupFailures(t *testing.T) {
	for _, test := range []struct {
		name, code string
		status     int
		err        error
		panic      bool
	}{
		{"missing address", "ip_not_found", http.StatusNotFound, geo.ErrNotFound, false},
		{"internal error", "internal_error", http.StatusInternalServerError, errors.New("secret internal filesystem path"), false},
		{"panic", "internal_error", http.StatusInternalServerError, nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := New(locatorFunc(func(netip.Addr) (geo.Location, error) {
				if test.panic {
					panic("secret internal panic")
				}
				return geo.Location{}, test.err
			}), quietLogger(), func() bool { return true })
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/8.8.8.8", nil))
			assertError(t, recorder, test.status, test.code)
			if strings.Contains(recorder.Body.String(), "secret") {
				t.Fatal("internal details leaked to the client")
			}
		})
	}
}

func TestRoutesAndReadiness(t *testing.T) {
	for _, test := range []struct {
		method, path, code string
		ready              bool
		status             int
	}{
		{http.MethodGet, "/healthz", "", false, http.StatusOK},
		{http.MethodGet, "/readyz", "", true, http.StatusOK},
		{http.MethodGet, "/readyz", "not_ready", false, http.StatusServiceUnavailable},
		{http.MethodGet, "/8.8.8.8", "not_ready", false, http.StatusServiceUnavailable},
		{http.MethodGet, "/", "not_ready", false, http.StatusServiceUnavailable},
		{http.MethodPost, "/8.8.8.8", "method_not_allowed", true, http.StatusMethodNotAllowed},
		{http.MethodDelete, "/", "method_not_allowed", true, http.StatusMethodNotAllowed},
		{http.MethodDelete, "/healthz", "method_not_allowed", true, http.StatusMethodNotAllowed},
		{http.MethodGet, "/not/a/route", "route_not_found", true, http.StatusNotFound},
		{http.MethodHead, "/healthz", "", true, http.StatusOK},
		{http.MethodHead, "/8.8.8.8", "", true, http.StatusOK},
		{http.MethodHead, "/", "", true, http.StatusOK},
	} {
		t.Run(test.method+test.path+test.code, func(t *testing.T) {
			handler := New(locatorFunc(func(netip.Addr) (geo.Location, error) {
				if !test.ready {
					t.Fatal("lookup was called while unready")
				}
				return geo.Location{}, nil
			}), quietLogger(), func() bool { return test.ready })
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, nil))
			if test.code != "" {
				assertError(t, recorder, test.status, test.code)
			} else if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d", recorder.Code, test.status)
			}
			if test.status == http.StatusMethodNotAllowed {
				want := "GET, HEAD"
				if test.path == "/" {
					want = "GET, HEAD, POST"
				}
				if recorder.Header().Get("Allow") != want {
					t.Fatalf("Allow = %q, want %q", recorder.Header().Get("Allow"), want)
				}
			}
			if test.method == http.MethodHead && recorder.Body.Len() != 0 {
				t.Fatal("HEAD returned a body")
			}
			if test.status == http.StatusOK && (test.path == "/healthz" || test.path == "/readyz") && recorder.Header().Get("Cache-Control") != noStoreCacheControl {
				t.Fatalf("operational endpoint Cache-Control = %q", recorder.Header().Get("Cache-Control"))
			}
		})
	}
}

func assertError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var response ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("invalid JSON: %v; body: %s", err, recorder.Body)
	}
	if recorder.Code != status || response.Error.Code != code || response.Error.Message == "" {
		t.Fatalf("got status %d, response %+v; want %d / %s", recorder.Code, response, status, code)
	}
	if got := recorder.Header().Get("Cache-Control"); got != noStoreCacheControl {
		t.Fatalf("error Cache-Control = %q, want %q", got, noStoreCacheControl)
	}
}
