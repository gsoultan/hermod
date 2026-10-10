package lookup

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/infra/filestorage"
	xlsx "github.com/tealeg/xlsx"
)

const countriesCSV = "code,name,region\nID,Indonesia,Asia\nFR,France,Europe\n"

// allowReferenceDir adds dir to the directories reference_lookup may read,
// for the rest of the test.
func allowReferenceDir(t *testing.T, dir string) {
	t.Helper()
	old := currentReferenceRoots()
	SetReferenceRoots(append(slices.Clone(old), dir)...)
	t.Cleanup(func() { SetReferenceRoots(old...) })
}

// writeFile writes a reference file in a directory the node may read.
func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	allowReferenceDir(t, dir)
	return writeFileIn(t, dir, name, content)
}

func writeFileIn(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return p
}

func refMsg(t *testing.T, fields map[string]any) hermod.Message {
	t.Helper()
	msg := message.AcquireMessage()
	t.Cleanup(msg.Release)
	for k, v := range fields {
		msg.SetData(k, v)
	}
	return msg
}

func runReference(t *testing.T, msg hermod.Message, cfg map[string]any) (hermod.Message, error) {
	t.Helper()
	tr, ok := transformer.Get("reference_lookup")
	if !ok {
		t.Fatal("reference_lookup is not registered")
	}
	return tr.Transform(t.Context(), msg, cfg)
}

func TestReferenceLookupCSV(t *testing.T) {
	path := writeFile(t, "countries.csv", countriesCSV)
	tests := []struct {
		name string
		cfg  map[string]any
		want map[string]any
	}{
		{
			name: "every other column onto the record",
			cfg:  map[string]any{},
			want: map[string]any{"country": "FR", "name": "France", "region": "Europe"},
		},
		{
			name: "chosen columns under a target field",
			cfg:  map[string]any{"columns": "name", "targetField": "ref"},
			want: map[string]any{"country": "FR", "ref": map[string]any{"name": "France"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := map[string]any{"filePath": path, "keyColumn": "code", "keyField": "country"}
			for k, v := range tc.cfg {
				cfg[k] = v
			}
			out, err := runReference(t, refMsg(t, map[string]any{"country": "FR"}), cfg)
			if err != nil {
				t.Fatalf("reference_lookup: %v", err)
			}
			if got := out.Data(); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("data = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestReferenceLookupExcel(t *testing.T) {
	f := xlsx.NewFile()
	if _, err := f.AddSheet("ignored"); err != nil {
		t.Fatal(err)
	}
	sh, err := f.AddSheet("prices")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range [][]string{{"sku", "price"}, {"x", "9.50"}} {
		row := sh.AddRow()
		for _, v := range r {
			row.AddCell().Value = v
		}
	}
	dir := t.TempDir()
	allowReferenceDir(t, dir)
	path := filepath.Join(dir, "prices.xlsx")
	if err := f.Save(path); err != nil {
		t.Fatal(err)
	}

	out, err := runReference(t, refMsg(t, map[string]any{"sku": "x"}), map[string]any{
		"filePath": path, "sheet": "prices", "keyColumn": "sku", "keyField": "sku",
	})
	if err != nil {
		t.Fatalf("reference_lookup: %v", err)
	}
	if got := out.Data()["price"]; got != "9.50" {
		t.Errorf("price = %v, want 9.50 from the named sheet", got)
	}
}

func TestReferenceLookupReloadsWhenTheFileChanges(t *testing.T) {
	old := referenceStatEvery
	referenceStatEvery = 0
	t.Cleanup(func() { referenceStatEvery = old })

	path := writeFile(t, "rates.csv", "cur,rate\nEUR,1.1\n")
	cfg := map[string]any{"filePath": path, "keyColumn": "cur", "keyField": "cur"}
	lookup := func() any {
		out, err := runReference(t, refMsg(t, map[string]any{"cur": "EUR"}), cfg)
		if err != nil {
			t.Fatalf("reference_lookup: %v", err)
		}
		return out.Data()["rate"]
	}
	if got := lookup(); got != "1.1" {
		t.Fatalf("rate = %v, want 1.1", got)
	}

	if err := os.WriteFile(path, []byte("cur,rate\nEUR,1.2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Same size, so only the modification time tells the two apart.
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if got := lookup(); got != "1.2" {
		t.Errorf("rate = %v after the file changed, want 1.2", got)
	}
}

func TestReferenceLookupMiss(t *testing.T) {
	path := writeFile(t, "countries.csv", countriesCSV)
	base := map[string]any{"filePath": path, "keyColumn": "code", "keyField": "country"}
	with := func(extra map[string]any) map[string]any {
		cfg := map[string]any{}
		for k, v := range base {
			cfg[k] = v
		}
		for k, v := range extra {
			cfg[k] = v
		}
		return cfg
	}

	out, err := runReference(t, refMsg(t, map[string]any{"country": "ZZ"}), with(nil))
	if err != nil || !reflect.DeepEqual(out.Data(), map[string]any{"country": "ZZ"}) {
		t.Errorf("passthrough: %v, %v; want the record unchanged", out.Data(), err)
	}

	if _, err := runReference(t, refMsg(t, map[string]any{"country": "ZZ"}), with(map[string]any{"onMiss": "fail"})); err == nil ||
		!strings.Contains(err.Error(), "ZZ") {
		t.Errorf("fail: err = %v, want one naming the key", err)
	}

	out, err = runReference(t, refMsg(t, map[string]any{}), with(map[string]any{"onMiss": "default", "defaultValue": "unknown", "targetField": "ref"}))
	if err != nil || out.Data()["ref"] != "unknown" {
		t.Errorf("default: %v, %v; want ref = unknown", out.Data(), err)
	}
}

func TestReferenceLookupRefuses(t *testing.T) {
	csvPath := writeFile(t, "countries.csv", countriesCSV)
	tests := []struct {
		name    string
		cfg     map[string]any
		wantErr string
	}{
		{"no file", map[string]any{"keyColumn": "code", "keyField": "c"}, "filePath"},
		{"an object-store path", map[string]any{"filePath": "s3://bucket/x.csv", "keyColumn": "code", "keyField": "c"}, "local"},
		{"a directory", map[string]any{"filePath": filepath.Dir(csvPath), "format": "csv", "keyColumn": "code", "keyField": "c"}, "regular file"},
		{"a format it cannot read", map[string]any{"filePath": writeFile(t, "x.json", "{}"), "keyColumn": "code", "keyField": "c"}, "csv"},
		{"a key column the file lacks", map[string]any{"filePath": csvPath, "keyColumn": "iso", "keyField": "c"}, "iso"},
		{"a file over the size limit", map[string]any{"filePath": csvPath, "keyColumn": "code", "keyField": "c", "maxBytes": 10}, "10 bytes"},
		{"more rows than the limit", map[string]any{"filePath": csvPath, "keyColumn": "code", "keyField": "c", "maxRows": 1}, "1 rows"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runReference(t, refMsg(t, map[string]any{"c": "FR"}), tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.wantErr)
			}
		})
	}
}

// A reference file is read only from the upload storage root and the
// directories an operator allows. Every way out of them is refused: a plain
// path elsewhere, a ../ walk, and a symlink pointing out.
func TestReferenceLookupReadsOnlyAllowedDirectories(t *testing.T) {
	allowed := t.TempDir()
	allowReferenceDir(t, allowed)
	outside := t.TempDir()
	secret := writeFileIn(t, outside, "secret.csv", countriesCSV)

	// allowed/../<outside's name>/secret.csv, written out rather than joined,
	// because Join would clean the escape away before the node saw it.
	rel, err := filepath.Rel(filepath.Dir(allowed), secret)
	if err != nil {
		t.Fatal(err)
	}
	dotdot := allowed + string(filepath.Separator) + ".." + string(filepath.Separator) + rel

	link := filepath.Join(allowed, "link.csv")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	for name, path := range map[string]string{
		"a path outside":         secret,
		"a ../ escape":           dotdot,
		"a symlink pointing out": link,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := runReference(t, refMsg(t, map[string]any{"c": "FR"}),
				map[string]any{"filePath": path, "format": "csv", "keyColumn": "code", "keyField": "c"})
			if err == nil || !strings.Contains(err.Error(), "outside") {
				t.Fatalf("err = %v, want the path refused as outside the allowed directories", err)
			}
		})
	}
}

// A file saved by the upload endpoint's local storage is read from where the
// storage put it.
func TestReferenceLookupReadsAnUploadedFile(t *testing.T) {
	root := t.TempDir()
	store, err := filestorage.NewLocalStorage(root)
	if err != nil {
		t.Fatal(err)
	}
	old := currentReferenceRoots()
	ConfigureReferenceRoots(root)
	t.Cleanup(func() { SetReferenceRoots(old...) })

	path, err := store.Save(t.Context(), "countries-1.csv", strings.NewReader(countriesCSV))
	if err != nil {
		t.Fatal(err)
	}
	out, err := runReference(t, refMsg(t, map[string]any{"c": "ID"}),
		map[string]any{"filePath": path, "keyColumn": "code", "keyField": "c"})
	if err != nil {
		t.Fatalf("reference_lookup: %v", err)
	}
	if out.Data()["name"] != "Indonesia" {
		t.Errorf("name = %v, want Indonesia", out.Data()["name"])
	}
}

func TestReferenceRootsFromTheEnvironment(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	t.Setenv(referenceDirsEnv, a+", relative/dir ,"+b)
	old := currentReferenceRoots()
	ConfigureReferenceRoots("")
	t.Cleanup(func() { SetReferenceRoots(old...) })

	if got := currentReferenceRoots(); len(got) != 2 {
		t.Fatalf("roots = %v, want the two absolute directories (a relative one is ignored)", got)
	}
}
