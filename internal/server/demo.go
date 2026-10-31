package server

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/abjelosevic88/pocket-homelab-boards/internal/store"
)

// seedDemo fills the owner's account with a few lived-in boards (only on an
// empty box, only with DEMO_DATA): a work project, a home board shared with
// a second person, a trip and a study plan, plus one archived board.
func (s *Server) seedDemo(ctx context.Context, owner int64) error {
	if n, _ := s.store.ListBoards(ctx, owner, store.BoardFilter{}); len(n) > 0 {
		return nil
	}
	rng := rand.New(rand.NewSource(42))
	now := time.Now()
	day := func(d int, hh, mm int) string {
		t := startOfDay(now).AddDate(0, 0, d).Add(time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute)
		return t.Format(time.RFC3339)
	}
	ago := func(d int) string { return now.AddDate(0, 0, -d).UTC().Format(time.RFC3339Nano) }

	// A second person for the shared board.
	mia, err := s.store.GetUserByEmail(ctx, "mia@example.com")
	if err != nil {
		mia = &store.User{Email: "mia@example.com", Name: "Mia Novak", Role: "member"}
		if err := s.store.CreateUser(ctx, mia); err != nil {
			return err
		}
	}
	octx := context.WithValue(ctx, userKey, &store.User{ID: owner})

	type card struct {
		title, desc string
		due         string
		labels      []string
		members     []int64
		cover       string
		repeat      string
		comments    []string
		done        bool
	}
	type list struct {
		title string
		done  bool
		wip   int
		cards []card
	}
	type board struct {
		title, desc, color, emoji string
		labels                    [][2]string
		lists                     []list
		share                     bool
		starred, archived         bool
	}
	boards := []board{
		{title: "Website relaunch", desc: "The new site: content, design and the switch-over.", color: "0c66e4", emoji: "🚀", starred: true,
			labels: [][2]string{{"Design", "e774bb"}, {"Content", "f5cd47"}, {"Bug", "f87168"}, {"Feature", "579dff"}, {"Blocked", "fea362"}, {"Quick win", "4bce97"}},
			lists: []list{
				{title: "Ideas", cards: []card{
					{title: "Dark mode for the docs", labels: []string{"Feature"}},
					{title: "Customer stories page", desc: "Three short stories with photos.\n\n- [ ] Ask Ana's team\n- [ ] Pick three customers\n- [ ] Interview questions", labels: []string{"Content"}},
					{title: "Newsletter sign-up in the footer", labels: []string{"Quick win"}},
				}},
				{title: "To do", cards: []card{
					{title: "Write the About page", desc: "Who we are, what we do, the team photo.\n\n- [x] Outline\n- [ ] First draft\n- [ ] Review with Mia", due: day(2, 17, 0), labels: []string{"Content"}, members: []int64{owner}, comments: []string{"I'll take the first draft this week."}},
					{title: "Pricing table", desc: "Three plans, yearly toggle.", due: day(5, 12, 0), labels: []string{"Design", "Feature"}, cover: "9f8fef"},
					{title: "Set up redirects from the old URLs", desc: "Export the old sitemap first.", due: day(9, 17, 0), labels: []string{"Feature"}},
					{title: "Contact form sends twice", desc: "Happens on slow connections — the button isn't disabled while sending.", due: day(-1, 17, 0), labels: []string{"Bug"}, members: []int64{owner}, comments: []string{"Reproduced on the phone over 3G.", "Fix is small, adding it to today."}},
				}},
				{title: "Doing", wip: 3, cards: []card{
					{title: "Home page hero", desc: "New headline and illustration.\n\n- [x] Headline options\n- [x] Pick the illustration\n- [ ] Mobile layout\n- [ ] Final copy", due: day(0, 16, 0), labels: []string{"Design"}, members: []int64{owner}, cover: "579dff", comments: []string{"Going with option B for the headline."}},
					{title: "Image compression", desc: "WebP with fallbacks.", due: day(1, 12, 0), labels: []string{"Quick win"}},
				}},
				{title: "Review", cards: []card{
					{title: "Navigation on phones", desc: "Drawer instead of the top menu.", due: day(-2, 17, 0), labels: []string{"Design"}, comments: []string{"Looks good to me — one small thing: the close button is too small."}},
				}},
				{title: "Done", done: true, cards: []card{
					{title: "Pick the fonts", labels: []string{"Design"}, done: true},
					{title: "Domain and hosting", labels: []string{"Feature"}, done: true},
					{title: "Sitemap", labels: []string{"Content"}, done: true},
				}},
			}},
		{title: "Home", desc: "Chores, repairs and shopping — shared with Mia.", color: "c25100", emoji: "🏡", share: true, starred: true,
			labels: [][2]string{{"Urgent", "f87168"}, {"Weekend job", "f5cd47"}, {"Needs a pro", "9f8fef"}, {"Garden", "94c748"}},
			lists: []list{
				{title: "Repairs", cards: []card{
					{title: "Fix the dripping tap", desc: "Kitchen. Probably the washer.\n\n- [ ] Buy washers\n- [ ] Turn off the water first", due: day(3, 10, 0), labels: []string{"Weekend job"}, members: []int64{owner}},
					{title: "Boiler service", due: day(12, 9, 0), labels: []string{"Needs a pro"}, comments: []string{"Called them, they have a slot on the 12th."}},
					{title: "Replace the hallway bulb", labels: []string{}, members: []int64{0}},
				}},
				{title: "Regular chores", cards: []card{
					{title: "Change the bed sheets", due: day(1, 10, 0), repeat: "weekly", members: []int64{0}},
					{title: "Water the plants", due: day(0, 19, 0), repeat: "weekdays", labels: []string{"Garden"}, members: []int64{owner}},
					{title: "Pay the electricity bill", due: day(6, 12, 0), repeat: "monthly", labels: []string{"Urgent"}, members: []int64{owner}},
					{title: "Clean the fridge", due: day(-3, 18, 0), repeat: "monthly"},
				}},
				{title: "To buy", cards: []card{
					{title: "Dishwasher tablets"}, {title: "Light bulbs (E27, warm)"}, {title: "Bird seed", labels: []string{"Garden"}},
				}},
				{title: "Done", done: true, cards: []card{
					{title: "Hang the shelf", done: true, members: []int64{0}}, {title: "Clear the gutters", labels: []string{"Garden"}, done: true},
				}},
			}},
		{title: "Trip to Lisbon", desc: "Long weekend in spring.", color: "227d9b", emoji: "✈️",
			labels: [][2]string{{"Paid", "4bce97"}, {"Needs booking", "fea362"}, {"Must see", "e774bb"}},
			lists: []list{
				{title: "To book", cards: []card{
					{title: "Flights", due: day(14, 20, 0), labels: []string{"Needs booking"}, desc: "Friday evening out, Monday night back."},
					{title: "Place to stay", desc: "Alfama or Príncipe Real.\n\n- [ ] Shortlist three\n- [ ] Check reviews\n- [ ] Book", labels: []string{"Needs booking"}},
				}},
				{title: "To pack", cards: []card{{title: "Passport"}, {title: "Walking shoes"}, {title: "Charger + adapter"}}},
				{title: "Ideas", cards: []card{
					{title: "Tram 28", labels: []string{"Must see"}}, {title: "Pastéis de Belém", labels: []string{"Must see"}}, {title: "Day trip to Sintra"},
				}},
				{title: "Booked", done: true, cards: []card{{title: "Travel insurance", labels: []string{"Paid"}, done: true}}},
			}},
		{title: "Learning Spanish", desc: "Twenty minutes a day.", color: "5b7f24", emoji: "📚",
			labels: [][2]string{{"Grammar", "579dff"}, {"Speaking", "4bce97"}, {"Reading", "f5cd47"}},
			lists: []list{
				{title: "To learn", cards: []card{{title: "Subjunctive", labels: []string{"Grammar"}}, {title: "Numbers to 1000", labels: []string{"Grammar"}}}},
				{title: "Practising", cards: []card{{title: "Past tense verbs", labels: []string{"Grammar"}, due: day(4, 18, 0)}, {title: "Weekly call with Lucía", labels: []string{"Speaking"}, due: day(2, 19, 0), repeat: "weekly", members: []int64{owner}}}},
				{title: "Know it", done: true, cards: []card{{title: "Greetings", done: true}, {title: "Ordering food", labels: []string{"Speaking"}, done: true}}},
			}},
		{title: "Moving flat (2025)", desc: "All done.", color: "8590a2", emoji: "📦", archived: true,
			lists: []list{{title: "Done", done: true, cards: []card{{title: "Boxes", done: true}, {title: "Van", done: true}}}}},
	}

	for _, bd := range boards {
		b := store.Board{Title: bd.title, Description: bd.desc, Color: bd.color, Emoji: bd.emoji, Archived: bd.archived}
		if err := s.store.CreateBoard(ctx, owner, &b); err != nil {
			return err
		}
		if bd.starred {
			t := true
			_ = s.store.SetBoardPref(ctx, owner, b.ID, &t, nil, true)
		}
		if bd.share {
			if err := s.store.SetMember(ctx, b.ID, mia.ID, store.RoleEditor); err != nil {
				return err
			}
		}
		labelIDs := map[string]int64{}
		for _, l := range bd.labels {
			lb := store.Label{BoardID: b.ID, Title: l[0], Color: l[1]}
			if err := s.store.CreateLabel(ctx, &lb); err != nil {
				return err
			}
			labelIDs[l[0]] = lb.ID
		}
		for _, ld := range bd.lists {
			l := store.List{BoardID: b.ID, Title: ld.title, IsDone: ld.done, WIPLimit: ld.wip}
			if err := s.store.CreateList(ctx, &l, -1); err != nil {
				return err
			}
			for _, cd := range ld.cards {
				c := store.Card{BoardID: b.ID, ListID: l.ID, Title: cd.title, Description: cd.desc, DueAt: cd.due, CoverColor: cd.cover, Repeat: cd.repeat,
					CreatedAt: ago(5 + rng.Intn(40))}
				if cd.done || ld.done {
					c.DoneAt = ago(rng.Intn(10))
				}
				for _, n := range cd.labels {
					c.Labels = append(c.Labels, labelIDs[n])
				}
				for _, m := range cd.members {
					if m == 0 {
						m = mia.ID
					}
					c.Members = append(c.Members, m)
				}
				if err := s.store.CreateCard(ctx, owner, &c, -1); err != nil {
					return err
				}
				s.store.AddActivity(octx, b.ID, &c.ID, owner, "card_created", map[string]any{"title": c.Title, "list": l.Title}, "")
				for i, msg := range cd.comments {
					who := owner
					if bd.share && i%2 == 1 || (!bd.share && i%2 == 1) {
						who = mia.ID
					}
					cm := store.Comment{CardID: c.ID, Message: msg, CreatedAt: ago(3 - i)}
					if err := s.store.CreateComment(ctx, who, &cm); err != nil {
						return err
					}
				}
				if c.DoneAt != "" && rng.Intn(2) == 0 {
					_ = s.store.SaveWorklog(ctx, &store.Worklog{UserID: owner, CardID: c.ID, Date: now.AddDate(0, 0, -rng.Intn(7)).Format("2006-01-02"),
						DurationS: 900 * (1 + rng.Intn(8)), Source: "time", ExternalID: fmt.Sprintf("time:demo-%d", c.ID)})
				}
			}
		}
	}
	s.log.Info("demo data added", "boards", len(boards))
	return nil
}
