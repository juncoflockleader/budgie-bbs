package handler

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core/commandevents"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// --- World -----------------------------------------------------------------

type mudRoom struct {
	ID    string
	Name  string
	Desc  string
	Exits map[string]string // direction -> room id
}

const mudStartRoom = "square"

// mudWorld is the static map. Themed as the BudgieBBS town — a shared community
// hub players can wander and chat in. Kept in code for the MVP; can move to a
// DB/content source later without changing the command protocol.
var mudWorld = map[string]mudRoom{
	"square": {
		ID: "square", Name: "Town Square",
		Desc:  "A sunlit plaza at the heart of the BudgieBBS town. A worn notice board\nstands by the fountain, and footpaths lead off in every direction.",
		Exits: map[string]string{"n": "library", "s": "cafe", "e": "arcade", "w": "garden"},
	},
	"library": {
		ID: "library", Name: "The Library",
		Desc:  "Towering shelves of bound threads stretch into the rafters. Somewhere a\npage turns. The boards of the whole community seem to be archived here.",
		Exits: map[string]string{"s": "square"},
	},
	"cafe": {
		ID: "cafe", Name: "The Café",
		Desc:  "Mismatched chairs, the hiss of an espresso machine, and the low hum of\nconversation. A good place to linger and talk. A door east opens to bird-song.",
		Exits: map[string]string{"n": "square", "e": "aviary"},
	},
	"aviary": {
		ID: "aviary", Name: "The Aviary",
		Desc:  "A warm glass house full of budgerigars. They chatter and wheel overhead in\nflashes of green and gold, utterly unbothered by visitors.",
		Exits: map[string]string{"w": "cafe"},
	},
	"arcade": {
		ID: "arcade", Name: "The Arcade",
		Desc:  "Rows of humming cabinets glow in the dark. This is where the door games\nlive; the carpet is sticky with decades of spilled cola.",
		Exits: map[string]string{"w": "square"},
	},
	"garden": {
		ID: "garden", Name: "The Garden",
		Desc:  "A quiet green courtyard ringed with benches. A gravel path winds north\ntoward the sound of water.",
		Exits: map[string]string{"e": "square", "n": "pond"},
	},
	"pond": {
		ID: "pond", Name: "The Koi Pond",
		Desc:  "Fat orange koi drift beneath lily pads. The surface holds the whole sky.\nIt is very peaceful here.",
		Exits: map[string]string{"s": "garden"},
	},
}

// mudDirAliases maps user input to a canonical direction key.
var mudDirAliases = map[string]string{
	"n": "n", "north": "n",
	"s": "s", "south": "s",
	"e": "e", "east": "e",
	"w": "w", "west": "w",
	"u": "u", "up": "u",
	"d": "d", "down": "d",
}

var mudDirNames = map[string]string{"n": "north", "s": "south", "e": "east", "w": "west", "u": "up", "d": "down"}
var mudDirOpposite = map[string]string{"n": "the south", "s": "the north", "e": "the west", "w": "the east", "u": "below", "d": "above"}

// --- Occupancy (in-memory, per-node) ---------------------------------------

type mudOccupant struct {
	name     string
	roomID   string
	lastSeen time.Time
}

type mudOccupancyRegistry struct {
	mu sync.Mutex
	// keyed by userID; a user occupies at most one room at a time.
	byUser map[string]*mudOccupant
}

const mudOccupancyTTL = 15 * time.Minute

var (
	mudOccupancyOnce sync.Once
	mudOccupancyReg  *mudOccupancyRegistry
)

func mudOccupancy() *mudOccupancyRegistry {
	mudOccupancyOnce.Do(func() {
		mudOccupancyReg = &mudOccupancyRegistry{byUser: map[string]*mudOccupant{}}
	})
	return mudOccupancyReg
}

// touch records the player in a room, returning true if this is a fresh arrival
// into that room (so the caller announces an enter).
func (r *mudOccupancyRegistry) touch(userID, name, roomID string, now time.Time) (arrived bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur := r.byUser[userID]
	if cur == nil || cur.roomID != roomID {
		r.byUser[userID] = &mudOccupant{name: name, roomID: roomID, lastSeen: now}
		return true
	}
	cur.name = name
	cur.lastSeen = now
	return false
}

func (r *mudOccupancyRegistry) leave(userID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byUser, userID)
}

// occupants returns the names of other live players in roomID (excluding
// excludeUserID), dropping entries older than the TTL.
func (r *mudOccupancyRegistry) occupants(roomID, excludeUserID string, now time.Time) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var names []string
	for uid, occ := range r.byUser {
		if now.Sub(occ.lastSeen) > mudOccupancyTTL {
			delete(r.byUser, uid)
			continue
		}
		if occ.roomID != roomID || uid == excludeUserID {
			continue
		}
		names = append(names, occ.name)
	}
	sort.Strings(names)
	return names
}

// MUDLeaveUser removes a player from the world (e.g. on disconnect) without an
// announcement. Exported so the session layer can clean up on hard disconnects.
func MUDLeaveUser(userID string) { mudOccupancy().leave(strings.TrimSpace(userID)) }

// --- Player location persistence -------------------------------------------

func (h *Handler) mudPlayerRoom(userID string) string {
	var room string
	err := qQueryRow(h.db, `SELECT room_id FROM mud_players WHERE user_id=?`, userID).Scan(&room)
	if err != nil || strings.TrimSpace(room) == "" {
		return mudStartRoom
	}
	if _, ok := mudWorld[room]; !ok {
		return mudStartRoom
	}
	return room
}

func (h *Handler) mudSetPlayerRoom(userID, roomID string) error {
	_, err := projections.QExec(h.db,
		`INSERT INTO mud_players (user_id, room_id, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET room_id=excluded.room_id, updated_at=excluded.updated_at`,
		userID, roomID, nowMS())
	return err
}

// --- Command handling -------------------------------------------------------

func mudAck() Reply { return Reply{Result: &proto.AckResult{}} }

// mudPublishView delivers a private view/feedback event to the acting player.
// The command always runs on the player's own node, so local delivery suffices.
func (h *Handler) mudPublishView(userID string, v *proto.MUDViewPayload, now time.Time) {
	scopes, payload := commandevents.MUDView(userID, v, now.UnixMilli())
	h.bus.Publish(&proto.Event{Kind: proto.EvtMUDView, Scopes: scopes, Payload: payload, TS: payload.TS})
}

// executeMUDCommand parses and runs a single MUD command line for the actor.
// Effects are delivered as events: room actions broadcast to mud:room:<id>;
// the actor's own view/feedback goes privately to mud:user:<id>.
func (h *Handler) executeMUDCommand(actor *projections.User, p proto.MUDCommandPayload) Reply {
	if actor == nil || strings.TrimSpace(actor.ID) == "" {
		return Reply{Err: errDetail(proto.ErrForbidden, "sign in to enter the world", false)}
	}
	line := strings.TrimSpace(p.Line)
	verb, arg := mudSplit(line)
	now := time.Now()
	roomID := h.mudPlayerRoom(actor.ID)

	switch verb {
	case "quit", "leave", "exit":
		mudOccupancy().leave(actor.ID)
		h.mudBroadcast(roomID, "leave", actor.Name, actor.Name+" fades away.", now)
		h.mudPublishView(actor.ID, commandevents.MUDViewLeft(), now)

	case "help", "?", "commands":
		h.mudPublishView(actor.ID, commandevents.MUDViewLines(mudHelpLines()...), now)

	case "exits":
		h.mudPublishView(actor.ID, commandevents.MUDViewLines(mudExitsLine(roomID)), now)

	case "who", "here":
		others := mudOccupancy().occupants(roomID, actor.ID, now)
		line := "You are alone here."
		if len(others) > 0 {
			line = "Here with you: " + strings.Join(others, ", ")
		}
		h.mudPublishView(actor.ID, commandevents.MUDViewLines(line), now)

	case "say", "'":
		text := strings.TrimSpace(arg)
		if text == "" {
			h.mudPublishView(actor.ID, commandevents.MUDViewLines("Say what?"), now)
			break
		}
		h.mudEnsurePresence(actor, roomID, now)
		h.mudBroadcast(roomID, "say", actor.Name, actor.Name+` says, "`+text+`"`, now)

	case "emote", "pose", ":", "me":
		text := strings.TrimSpace(arg)
		if text == "" {
			h.mudPublishView(actor.ID, commandevents.MUDViewLines("Emote what?"), now)
			break
		}
		h.mudEnsurePresence(actor, roomID, now)
		h.mudBroadcast(roomID, "emote", actor.Name, actor.Name+" "+text, now)

	case "go":
		h.mudMove(actor, roomID, strings.TrimSpace(arg), now)

	case "", "look", "l":
		h.mudEnsurePresence(actor, roomID, now)
		h.mudPublishView(actor.ID, commandevents.MUDViewRoom(h.mudRoomView(roomID, actor.ID, now)), now)

	default:
		if _, ok := mudDirAliases[verb]; ok {
			h.mudMove(actor, roomID, verb, now)
			break
		}
		h.mudPublishView(actor.ID, commandevents.MUDViewLines(`Hm? Try "help" for a list of commands.`), now)
	}
	return mudAck()
}

// mudEnsurePresence registers the actor into the room, announcing an arrival the
// first time they appear there.
func (h *Handler) mudEnsurePresence(actor *projections.User, roomID string, now time.Time) {
	if mudOccupancy().touch(actor.ID, actor.Name, roomID, now) {
		h.mudBroadcast(roomID, "enter", actor.Name, actor.Name+" is here.", now)
	}
}

func (h *Handler) mudMove(actor *projections.User, fromRoom, rawDir string, now time.Time) {
	dir, ok := mudDirAliases[strings.ToLower(strings.TrimSpace(rawDir))]
	if !ok {
		h.mudPublishView(actor.ID, commandevents.MUDViewLines("Go where? Try a direction like north, south, east, west."), now)
		return
	}
	dest, ok := mudWorld[fromRoom].Exits[dir]
	if !ok {
		h.mudPublishView(actor.ID, commandevents.MUDViewLines("You can't go that way."), now)
		return
	}
	if err := h.mudSetPlayerRoom(actor.ID, dest); err != nil {
		h.mudPublishView(actor.ID, commandevents.MUDViewLines("You stumble and can't move right now."), now)
		return
	}
	mudOccupancy().touch(actor.ID, actor.Name, dest, now)
	h.mudBroadcast(fromRoom, "leave", actor.Name, actor.Name+" heads "+mudDirNames[dir]+".", now)
	h.mudBroadcast(dest, "enter", actor.Name, actor.Name+" arrives from "+mudDirOpposite[dir]+".", now)
	h.mudPublishView(actor.ID, commandevents.MUDViewRoom(h.mudRoomView(dest, actor.ID, now)), now)
}

func (h *Handler) mudRoomView(roomID, actorID string, now time.Time) *proto.MUDRoomView {
	room := mudWorld[roomID]
	exits := make([]string, 0, len(room.Exits))
	for dir := range room.Exits {
		exits = append(exits, mudDirNames[dir])
	}
	sort.Strings(exits)
	return &proto.MUDRoomView{
		ID:        room.ID,
		Name:      room.Name,
		Desc:      room.Desc,
		Exits:     exits,
		Occupants: mudOccupancy().occupants(roomID, actorID, now),
	}
}

// mudBroadcast publishes a live, room-scoped MUD event to everyone in the room
// (and sibling nodes), mirroring the ephemeral chat path.
func (h *Handler) mudBroadcast(roomID, kind, actor, text string, now time.Time) {
	ts := now.UnixMilli()
	scopes, payload := commandevents.MUDRoom(roomID, kind, actor, text, ts)
	h.bus.Publish(&proto.Event{
		Kind:    proto.EvtMUDRoom,
		Scopes:  scopes,
		Payload: payload,
		TS:      ts,
	})
	pgNotifyEphemeral(h.db, string(proto.EvtMUDRoom), newID("mud_"), strings.Join(scopes, ","))
}

func mudExitsLine(roomID string) string {
	room := mudWorld[roomID]
	if len(room.Exits) == 0 {
		return "There are no obvious exits."
	}
	dirs := make([]string, 0, len(room.Exits))
	for dir := range room.Exits {
		dirs = append(dirs, mudDirNames[dir])
	}
	sort.Strings(dirs)
	return "Exits: " + strings.Join(dirs, ", ")
}

func mudHelpLines() []string {
	return []string{
		"Commands:",
		"  look (l)            — describe the room",
		"  north/south/east/west, up/down (n/s/e/w/u/d) — move",
		"  go <direction>      — move",
		"  say <text> ('text)  — speak to the room",
		"  emote <text> (:text)— act (\"alice waves\")",
		"  who / here          — who else is in the room",
		"  exits               — list exits",
		"  quit                — leave the world",
	}
}

// mudSplit splits a command line into a lowercase verb and the remaining
// argument. It also peels leading punctuation shortcuts (' for say, : for emote).
func mudSplit(line string) (verb, arg string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", ""
	}
	switch line[0] {
	case '\'':
		return "say", strings.TrimSpace(line[1:])
	case ':':
		return "emote", strings.TrimSpace(line[1:])
	}
	if i := strings.IndexAny(line, " \t"); i >= 0 {
		return strings.ToLower(line[:i]), strings.TrimSpace(line[i+1:])
	}
	return strings.ToLower(line), ""
}
