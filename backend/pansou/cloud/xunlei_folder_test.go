package cloud

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestXunleiCreateFolderResponseEnvelope(t *testing.T) {
	for _, test := range []struct {
		name, response string
		valid          bool
	}{
		{"wrapped", `{"file":{"id":"created-dir","kind":"drive#folder","name":"剧集","parent_id":"dest"}}`, true},
		{"direct", `{"id":"created-dir","kind":"drive#folder","name":"剧集","parent_id":"dest"}`, true},
		{"no-confirmation", `{"task":{"id":"task"}}`, false},
		{"missing-id", `{"file":{"kind":"drive#folder","name":"剧集"}}`, false},
		{"wrong-name", `{"file":{"id":"created-dir","kind":"drive#folder","name":"别的目录"}}`, false},
		{"wrong-kind", `{"file":{"id":"created-dir","kind":"drive#file","name":"剧集"}}`, false},
		{"empty-envelope", `{"id":"created-dir","kind":"drive#folder","name":"剧集","file":{}}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			writes := 0
			p := xunleiTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/files" {
					t.Fatalf("unexpected endpoint %s", r.URL.Path)
				}
				if r.Method == http.MethodGet {
					fmt.Fprint(w, `{"files":[]}`)
					return
				}
				writes++
				fmt.Fprint(w, test.response)
			})
			folder, err := p.CreateFolder(context.Background(), xunleiTestCredentials(), "dest", "剧集")
			if (err == nil) != test.valid || writes != 1 {
				t.Fatalf("folder=%+v error=%v writes=%d", folder, err, writes)
			}
			if test.valid && (folder.ID != "created-dir" || folder.Name != "剧集") {
				t.Fatalf("wrong folder %+v", folder)
			}
		})
	}
}

func TestXunleiCreateFolderReusesDirectoryAfterUnconfirmedResponse(t *testing.T) {
	created, writes := false, 0
	p := xunleiTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if created {
				fmt.Fprint(w, `{"files":[{"id":"created-dir","kind":"drive#folder","name":"剧集"}]}`)
			} else {
				fmt.Fprint(w, `{"files":[]}`)
			}
			return
		}
		writes++
		created = true
		fmt.Fprint(w, `{}`)
	})
	if _, err := p.CreateFolder(context.Background(), xunleiTestCredentials(), "dest", "剧集"); err == nil {
		t.Fatal("unconfirmed create accepted")
	}
	folder, err := p.CreateFolder(context.Background(), xunleiTestCredentials(), "dest", "剧集")
	if err != nil || folder.ID != "created-dir" || writes != 1 {
		t.Fatalf("directory duplicated: %+v %v writes=%d", folder, err, writes)
	}
}
