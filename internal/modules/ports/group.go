package ports

import (
	"sort"
	"strconv"
	"strings"
)

// disp is one displayed line: a group leader (with members folded under it),
// an expanded member, or a plain row when grouping is off.
type disp struct {
	Row       Row
	Key       string // group key; "" for flat rows
	Members   []Row  // other listeners folded into this leader
	Member    bool   // this line is an expanded member
	Expanded  bool
	LeaderPID int // for members: the leader's pid, to show only differing pids
}

// groupKey folds the same service on the same port across addresses and IP
// families: process name when known (docker runs one docker-proxy per
// family, so pid would split them), else the identity name. tcp and udp
// stay apart because CONN only means something for tcp.
func groupKey(r Row) string {
	who := r.Identity.Name
	if r.Process != "" {
		who = r.Process
	}
	if r.Container != "" {
		who = "docker▸" + r.Container // every docker-proxy shares a name; the container is the identity
	}
	return strings.TrimSuffix(r.Proto, "6") + ":" + strconv.Itoa(r.Port) + ":" + who
}

// group folds rows into leaders. The wildcard address leads when present;
// members keep sort order. Counts and markers aggregate onto the leader.
func group(rows []Row, expanded map[string]bool) []disp {
	order := []string{}
	byKey := map[string][]Row{}
	for _, r := range rows {
		k := groupKey(r)
		if _, ok := byKey[k]; !ok {
			order = append(order, k)
		}
		byKey[k] = append(byKey[k], r)
	}
	out := make([]disp, 0, len(order))
	for _, k := range order {
		members := byKey[k]
		sort.SliceStable(members, func(i, j int) bool { return wildcard(members[i]) && !wildcard(members[j]) })
		lead := members[0]
		lead.Peers = append([]Peer(nil), lead.Peers...)
		for _, m := range members[1:] {
			lead.Conn += m.Conn
			lead.New = lead.New || m.New
			lead.Gone = lead.Gone && m.Gone
			lead.Loopback = lead.Loopback && m.Loopback
			if m.Proto != lead.Proto {
				// Both families: show the bare proto; members carry the 6.
				lead.Proto = strings.TrimSuffix(lead.Proto, "6")
			}
			if len(lead.Peers) < 10 {
				lead.Peers = append(lead.Peers, m.Peers...)
			}
		}
		d := disp{Row: lead, Key: k, Members: members[1:], Expanded: expanded[k]}
		out = append(out, d)
		if d.Expanded {
			for _, m := range members[1:] {
				out = append(out, disp{Row: m, Key: k, Member: true, LeaderPID: lead.PID})
			}
		}
	}
	return out
}

func wildcard(r Row) bool { return r.Addr == "0.0.0.0" || r.Addr == "::" || r.Addr == "*" }

func flat(rows []Row) []disp {
	out := make([]disp, len(rows))
	for i, r := range rows {
		out[i] = disp{Row: r}
	}
	return out
}
