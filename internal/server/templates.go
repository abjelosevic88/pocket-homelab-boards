package server

import "net/http"

// template is a board to start from (also the shape of an AI draft).
type template struct {
	Key         string          `json:"key,omitempty"`
	Title       string          `json:"title"`
	Description string          `json:"description,omitempty"`
	Emoji       string          `json:"emoji"`
	Color       string          `json:"color"`
	Lists       []templateList  `json:"lists"`
	Labels      []templateLabel `json:"labels"`
}

type templateList struct {
	Title  string   `json:"title"`
	IsDone bool     `json:"is_done"`
	Cards  []string `json:"cards"`
}

type templateLabel struct {
	Title string `json:"title"`
	Color string `json:"color"`
}

var defaultLabels = []templateLabel{
	{"Important", "f87168"}, {"Waiting", "f5cd47"}, {"Idea", "9f8fef"},
	{"Quick", "4bce97"}, {"Home", "6cc3e0"}, {"Work", "579dff"},
}

func defaultTemplate() template {
	return template{Lists: []templateList{{Title: "To do"}, {Title: "Doing"}, {Title: "Done", IsDone: true}}, Labels: defaultLabels}
}

var templates = []template{
	{Key: "kanban", Title: "Project", Description: "Ideas to done, with a column for what is waiting on someone.", Emoji: "🚀", Color: "0c66e4",
		Lists:  []templateList{{Title: "Ideas"}, {Title: "To do"}, {Title: "Doing"}, {Title: "Waiting"}, {Title: "Done", IsDone: true}},
		Labels: []templateLabel{{"Bug", "f87168"}, {"Feature", "579dff"}, {"Design", "e774bb"}, {"Blocked", "fea362"}, {"Quick win", "4bce97"}}},
	{Key: "week", Title: "This week", Description: "One column per day and a place for whatever comes up.", Emoji: "🗓️", Color: "1f845a",
		Lists:  []templateList{{Title: "Inbox"}, {Title: "Monday"}, {Title: "Tuesday"}, {Title: "Wednesday"}, {Title: "Thursday"}, {Title: "Friday"}, {Title: "Weekend"}, {Title: "Done", IsDone: true}},
		Labels: []templateLabel{{"Work", "579dff"}, {"Home", "6cc3e0"}, {"Errand", "fea362"}, {"Important", "f87168"}}},
	{Key: "home", Title: "Home", Description: "Chores, repairs and the things to buy, shared with the people you live with.", Emoji: "🏡", Color: "c25100",
		Lists: []templateList{
			{Title: "Repairs", Cards: []string{"Fix the dripping tap", "Replace the hallway bulb"}},
			{Title: "Regular chores", Cards: []string{"Change the bed sheets", "Clean the fridge", "Water the plants"}},
			{Title: "To buy", Cards: []string{"Light bulbs", "Dishwasher tablets"}},
			{Title: "Done", IsDone: true}},
		Labels: []templateLabel{{"Urgent", "f87168"}, {"Weekend job", "f5cd47"}, {"Needs a pro", "9f8fef"}, {"Garden", "94c748"}}},
	{Key: "trip", Title: "Trip", Description: "From bookings to packing to the day you leave.", Emoji: "✈️", Color: "227d9b",
		Lists: []templateList{
			{Title: "To book", Cards: []string{"Flights", "Place to stay", "Travel insurance"}},
			{Title: "To pack", Cards: []string{"Passport", "Chargers", "Medicine"}},
			{Title: "Ideas", Cards: []string{"Things to see", "Places to eat"}},
			{Title: "Booked", IsDone: true}},
		Labels: []templateLabel{{"Paid", "4bce97"}, {"Needs booking", "fea362"}, {"Must see", "e774bb"}}},
	{Key: "content", Title: "Writing", Description: "From an idea to published.", Emoji: "✍️", Color: "6e5dc6",
		Lists:  []templateList{{Title: "Ideas"}, {Title: "Drafting"}, {Title: "Editing"}, {Title: "Ready"}, {Title: "Published", IsDone: true}},
		Labels: []templateLabel{{"Blog", "579dff"}, {"Newsletter", "9f8fef"}, {"Social", "e774bb"}, {"Long read", "f5cd47"}}},
	{Key: "hiring", Title: "Hiring", Description: "Candidates moving from applied to hired.", Emoji: "🤝", Color: "ae2e24",
		Lists:  []templateList{{Title: "Applied"}, {Title: "Phone screen"}, {Title: "Interview"}, {Title: "Offer"}, {Title: "Hired", IsDone: true}, {Title: "Not this time"}},
		Labels: []templateLabel{{"Referral", "4bce97"}, {"Senior", "579dff"}, {"Remote", "6cc3e0"}}},
	{Key: "study", Title: "Study plan", Description: "Topics to learn, practise and review.", Emoji: "📚", Color: "5b7f24",
		Lists:  []templateList{{Title: "To learn"}, {Title: "Learning"}, {Title: "To review"}, {Title: "Know it", IsDone: true}},
		Labels: []templateLabel{{"Exam", "f87168"}, {"Reading", "f5cd47"}, {"Practice", "4bce97"}}},
}

func templateByKey(k string) (template, bool) {
	for _, t := range templates {
		if t.Key == k {
			return t, true
		}
	}
	return template{}, false
}

func (s *Server) handleTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, templates)
}
