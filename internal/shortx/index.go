package shortx

const (
	OfficialIndexURL  = "https://github.com/ShortX-Repo/ShortX-Files/blob/main/index.json"
	SnowIndexURL      = "https://raw.githubusercontent.com/snowzlmbot/ShortX-Files/main/index.json"
	PreferredIndexURL = SnowIndexURL
)

type IndexSources struct {
	Official  string `json:"official"`
	Snow      string `json:"snow"`
	Preferred string `json:"preferred"`
}

func Sources() IndexSources {
	return IndexSources{Official: OfficialIndexURL, Snow: SnowIndexURL, Preferred: PreferredIndexURL}
}
