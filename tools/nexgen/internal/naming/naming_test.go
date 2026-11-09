package naming

import "testing"

func TestDefKey(t *testing.T) {
	for _, tc := range []struct{ pkg, fqn, want string }{
		{"a.v1", "a.v1.Namespace", "Namespace"},
		{"a.v1", "a.v1.CodecServerSpec.CustomErrorMessage.ErrorMessage", "CodecServerSpecCustomErrorMessageErrorMessage"},
	} {
		if got := DefKey(tc.pkg, tc.fqn); got != tc.want {
			t.Errorf("DefKey(%q, %q) = %q, want %q", tc.pkg, tc.fqn, got, tc.want)
		}
	}
}

func TestFilePath(t *testing.T) {
	for _, tc := range []struct {
		pkg     string
		service bool
		ext     string
		want    string
		wantErr bool
	}{
		{"temporal.api.cloud.namespace.v1", false, "yaml", "temporal/api/cloud/namespace/v1/namespace.yaml", false},
		{"temporal.api.cloud.cloudservice.v1", true, "yaml", "temporal/api/cloud/cloudservice/v1/cloudservice.nexusrpc.yaml", false},
		{"a.b.v1beta2", false, "json", "a/b/v1beta2/b.json", false},
		{"a.b", false, "yaml", "a/b/b.yaml", false},
		{"v1", false, "yaml", "", true},
		{"", false, "yaml", "", true},
	} {
		got, err := FilePath(tc.pkg, tc.service, tc.ext)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("FilePath(%q, %v, %q) = %q, %v; want %q, error %v", tc.pkg, tc.service, tc.ext, got, err, tc.want, tc.wantErr)
		}
	}
}

func TestKeywordOverrides(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want []KeywordOverride
	}{
		{"name", nil},
		{"type", nil},
		{"default", []KeywordOverride{{"x-java-name", "default_"}, {"x-ts-name", "default_"}}},
		{"from", []KeywordOverride{{"x-py-name", "from_"}}},
		{"class", []KeywordOverride{{"x-java-name", "class_"}, {"x-py-name", "class_"}, {"x-ts-name", "class_"}}},
		{"fromAddress", nil},
		{"items", []KeywordOverride{{"x-java-name", "items_"}}},
		{"element2", []KeywordOverride{{"x-java-name", "element2_"}}},
		{"element0", nil},
	} {
		got := KeywordOverrides(tc.key, true)
		if len(got) != len(tc.want) {
			t.Errorf("KeywordOverrides(%q) = %v, want %v", tc.key, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("KeywordOverrides(%q) = %v, want %v", tc.key, got, tc.want)
			}
		}
	}
}

func TestKeywordOverridesOperation(t *testing.T) {
	got := KeywordOverrides("delete", false)
	want := []KeywordOverride{{"x-ts-name", "delete_"}}
	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("KeywordOverrides(delete, false) = %v, want %v", got, want)
	}
	if got := KeywordOverrides("items", false); got != nil {
		t.Errorf("an operation key is not a deserializer local; got %v", got)
	}
}

func TestOperationKey(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"GetNamespace", "getNamespace"},
		{"GetAPIKey", "getAPIKey"},
	} {
		if got := OperationKey(tc.in); got != tc.want {
			t.Errorf("OperationKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
