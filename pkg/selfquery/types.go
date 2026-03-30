package selfquery

type Options struct {
	Model string

	AllowedFields []string

	MaxJSONChars int
}

type FilterSpec struct {
	Namespace string   `json:"namespace,omitempty"`
	Source    string   `json:"source,omitempty"`
	DocType   string   `json:"doc_type,omitempty"`
	Location  string   `json:"location,omitempty"`
	Years     []string `json:"years,omitempty"`
	Dates     []string `json:"dates,omitempty"`
	TagsAny   []string `json:"tags_any,omitempty"`
}

type Result struct {
	Query   string
	Filters map[string]any

	Spec FilterSpec
	Raw  string
}
