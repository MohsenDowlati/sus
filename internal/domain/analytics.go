package domain

type DailyClickTotal struct {
	Date        string `bson:"date" json:"date"`
	TotalClicks int64  `bson:"total_clicks" json:"total_clicks"`
}

type LinkAnalytics struct {
	Code        string            `json:"code"`
	From        string            `json:"from"`
	To          string            `json:"to"`
	TotalClicks int64             `json:"total_clicks"`
	Daily       []DailyClickTotal `json:"daily"`
	Referrers   map[string]int64  `json:"referrers"`
	Browsers    map[string]int64  `json:"browsers"`
}
