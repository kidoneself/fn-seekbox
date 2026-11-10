package cloud

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type checkTransport func(*http.Request) (*http.Response, error)

func (f checkTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestBaiduCheckDeletionRequiresReceipt(t *testing.T) {
	for _, fixture := range []struct {
		body string
		ok   bool
	}{{`{"request_id":123}`, true}, {`{"error_code":0,"request_id":"456"}`, true}, {`{}`, false}, {`{"error_code":0,"request_id":"0"}`, false}, {`{"error_code":31066,"request_id":123}`, false}} {
		t.Run(fixture.body, func(t *testing.T) {
			calls := 0
			p := NewBaiduProvider().(*baiduProvider)
			p.client = &http.Client{Transport: checkTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != "pcs.baidu.com" || r.URL.Path != "/rest/2.0/pcs/file" || r.URL.Query().Get("method") != "delete" {
					t.Fatal("wrong delete route")
				}
				if r.Header.Get("Cookie") != "BDUSS=owned" {
					t.Fatal("wrong credentials")
				}
				if err := r.ParseMultipartForm(1024); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(r.FormValue("param"), "/.seekbox-check-test/probe.txt") {
					t.Fatal("wrong deletion target")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fixture.body)), Header: make(http.Header)}, nil
			})}
			obj := checkObject{ID: "1", Name: "probe.txt", Size: 1}
			err := p.deleteCheckObject(context.Background(), Credentials{"cookie": "BDUSS=owned"}, "/.seekbox-check-test", obj)
			if (err == nil) != fixture.ok {
				t.Fatalf("receipt validation: %v", err)
			}
			if err = p.deleteCheckObject(context.Background(), Credentials{}, "/", obj); err == nil || calls != 1 {
				t.Fatal("root-file deletion must be blocked before HTTP")
			}
		})
	}
}
func TestBaiduRejectedTransferIsNotPendingTask(t *testing.T) {
	p := baiduTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/gettemplatevariable":
			io.WriteString(w, `{"errno":0,"result":{"uk":123,"bdstoken":"test"}}`)
		case "/api/list":
			io.WriteString(w, `{"errno":0,"list":[]}`)
		case "/share/transfer":
			io.WriteString(w, `{"errno":2}`)
		default:
			t.Fatal(r.URL.Path)
		}
	})
	result, err := p.SaveBatch(context.Background(), Credentials{"cookie": "BDUSS=owned"}, Share{ID: "100", Data: map[string]string{"owner": "200"}}, []Entry{serviceFile("11", "probe.txt")}, "/.seekbox-check-test")
	var rejected *baiduOperationError
	if result.Submitted || !errors.As(err, &rejected) || rejected.Code != 2 {
		t.Fatal("definite rejection treated as asynchronous write", result, err)
	}
}

func TestXunleiOwnShareRejectionIsNotSubmitted(t *testing.T) {
	p := xunleiTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/files":
			io.WriteString(w, `{"files":[]}`)
		case "/share/restore":
			w.WriteHeader(400)
			io.WriteString(w, `{"error_code":9,"error":"file_restore_own"}`)
		default:
			t.Fatal(r.URL.Path)
		}
	})
	entry := serviceFile("file", "probe.txt")
	entry.Token = ""
	result, err := p.SaveBatch(context.Background(), xunleiTestCredentials(), Share{ID: "share"}, []Entry{entry}, "dest")
	var failure *functionFailure
	if result.Submitted || !errors.As(err, &failure) || failure.code != "SOURCE_UNAVAILABLE" {
		t.Fatalf("incorrect rejection: %+v %v", result, err)
	}
}

func TestQuarkOwnShareHTTPErrorIsNotSubmitted(t *testing.T) {
	p := quarkTestProvider(t, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/1/clouddrive/file/sort":
			return quarkTestResponse(`{"code":0,"status":200,"data":{"list":[]},"metadata":{"_total":0}}`), nil
		case "/1/clouddrive/share/sharepage/save":
			resp := quarkTestResponse(`{"code":41017,"status":404,"message":"private upstream detail"}`)
			resp.StatusCode = 404
			return resp, nil
		default:
			t.Fatal(r.URL.Path)
			return nil, nil
		}
	})
	result, err := p.SaveBatch(context.Background(), quarkTestCredentials, Share{ID: "share", Data: map[string]string{"stoken": "private"}}, []Entry{serviceFile("file", "probe.txt")}, "dest")
	var failure *functionFailure
	if result.Submitted || !errors.As(err, &failure) || failure.code != "SOURCE_UNAVAILABLE" || strings.Contains(err.Error(), "private") {
		t.Fatalf("incorrect rejection: %+v %v", result, err)
	}
}

func TestQuarkCheckWaitsForConsistentListingAfterDelete(t *testing.T) {
	calls := 0
	p := quarkTestProvider(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Query().Get("_t") == "" {
			t.Fatal("missing fresh-list parameter")
		}
		if calls <= 2 {
			return quarkTestResponse(`{"code":0,"status":200,"data":{"list":[]},"metadata":{"_total":1}}`), nil
		}
		return quarkTestResponse(`{"code":0,"status":200,"data":{"list":[]},"metadata":{"_total":0}}`), nil
	})
	files, err := p.checkFiles(context.Background(), quarkTestCredentials, "dest")
	if err != nil || len(files) != 0 || calls != 3 {
		t.Fatalf("list not confirmed: calls=%d err=%v", calls, err)
	}
}

func TestBaiduCheckHTTPUnauthorizedExpiresAccount(t *testing.T) {
	p := NewBaiduProvider().(*baiduProvider)
	p.client = &http.Client{Transport: checkTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("Unauthorized")), Header: make(http.Header)}, nil
	})}
	err := p.deleteCheckObject(context.Background(), Credentials{"cookie": "BDUSS=owned"}, "/.seekbox-check-test", checkObject{ID: "1", Name: "probe.txt", Size: 1})
	if !errors.Is(err, ErrAuthExpired) {
		t.Fatal("401 not classified as expired", err)
	}
}
