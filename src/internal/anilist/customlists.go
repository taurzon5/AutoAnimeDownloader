package anilist

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// CustomListMember is an entry of an AniList custom list as GetCustomListMembers reads it: no media
// data, only what decides membership and where the pass's episode window starts.
type CustomListMember struct {
	EntryID  int
	MediaID  int
	Status   MediaListStatus
	Progress int
	// Lists is every custom list the entry is in, not only the requested ones: the excluded-list
	// check reads it (an anime in a download list and an excluded one stays out).
	Lists []string
}

const (
	// customListMembersPerChunk is AniList's cap for MediaListCollection's perChunk.
	customListMembersPerChunk = 500
	maxCustomListChunks       = 20
)

var (
	customListMembersCache = newTTLCache[[]CustomListMember]()
	// customListMembersFailed remembers a failed read with no cached copy for a minute: the pass asks
	// twice (the extras, then the deletion veto), and asking again right after a failure only burns
	// budget (decisions/072).
	customListMembersFailed = newTTLCache[string]()
)

const customListMembersFailTTL = 60 * time.Second

// GetCustomListMembers returns the entries of an account's named custom lists, whatever their list
// status (download custom lists, decisions/162). It reads MediaListCollection, which returns the
// account's lists in chunks of 500 entries and is asked only for the ids, the status, the progress
// and the list names: ceil(N/500) requests for an account of N entries, where a status_in query of
// the statuses outside download_statuses costs ceil(N/50) for the entries plus as many again for
// customLists (decisions/130 measured 1124 entries, 23 pages each). Cached customListsTTL; on
// failure the expired read is served with ErrFromCache, as for the other lists (decisions/072).
func GetCustomListMembers(userName string, lists []string, priority Priority) ([]CustomListMember, error) {
	key := userName + "\x00" + strings.Join(lists, "\x00")
	if cached, ok := customListMembersCache.get(key); ok {
		return cached, nil
	}
	if msg, ok := customListMembersFailed.get(key); ok {
		return staleCustomListMembers(key, errors.New(msg))
	}

	query := `query($u:String,$chunk:Int,$per:Int){MediaListCollection(userName:$u,type:ANIME,chunk:$chunk,perChunk:$per){hasNextChunk lists{name isCustomList entries{id mediaId status progress}}}}`
	type entry struct {
		ID       int             `json:"id"`
		MediaID  int             `json:"mediaId"`
		Status   MediaListStatus `json:"status"`
		Progress int             `json:"progress"`
	}
	type response struct {
		Data struct {
			MediaListCollection struct {
				HasNextChunk bool `json:"hasNextChunk"`
				Lists        []struct {
					Name         string  `json:"name"`
					IsCustomList bool    `json:"isCustomList"`
					Entries      []entry `json:"entries"`
				} `json:"lists"`
			} `json:"MediaListCollection"`
		} `json:"data"`
	}

	byEntry := map[int]*CustomListMember{}
	var order []int
	requested := map[int]bool{}
	for chunk := 1; ; chunk++ {
		if chunk > maxCustomListChunks {
			return staleCustomListMembers(key, fmt.Errorf("anilist: custom lists still have more chunks after %d", maxCustomListChunks))
		}
		resp, err := sendAnilistRequest[response](query, RequestVariables{"u": userName, "chunk": chunk, "per": customListMembersPerChunk}, priority)
		if err == nil && chunk == 1 && len(resp.Data.MediaListCollection.Lists) == 0 {
			// An account with entries always has lists: none at all is AniList degrading the field
			// (decisions/065), and "no members" would let status deletion reach the listed anime.
			err = errors.New("anilist: the media list collection came back empty")
		}
		if err != nil {
			if !errors.Is(err, ErrBudgetLow) {
				customListMembersFailed.set(key, err.Error(), customListMembersFailTTL)
			}
			return staleCustomListMembers(key, err)
		}
		for _, list := range resp.Data.MediaListCollection.Lists {
			if !list.IsCustomList {
				continue
			}
			for _, e := range list.Entries {
				m, ok := byEntry[e.ID]
				if !ok {
					m = &CustomListMember{EntryID: e.ID, MediaID: e.MediaID, Status: e.Status, Progress: e.Progress}
					byEntry[e.ID] = m
					order = append(order, e.ID)
				}
				if !slices.Contains(m.Lists, list.Name) {
					m.Lists = append(m.Lists, list.Name)
				}
				if slices.Contains(lists, list.Name) {
					requested[e.ID] = true
				}
			}
		}
		if !resp.Data.MediaListCollection.HasNextChunk {
			break
		}
	}

	members := make([]CustomListMember, 0, len(order))
	for _, id := range order {
		if requested[id] {
			members = append(members, *byEntry[id])
		}
	}
	ttl := customListsTTL
	if len(members) == 0 {
		// Empty can also be a degraded answer: keep it as briefly as an empty customLists map (decisions/011).
		ttl = customListsEmptyTTL
	}
	customListMembersCache.set(key, members, ttl)
	markCacheDirty()
	return members, nil
}

// staleCustomListMembers serves the expired read of a failed fetch, or the error when there is none.
// The budget gate's refusal serves it with no error: the refusal was ours and the read is minutes
// old. Only a real failure carries ErrFromCache (decisions/092).
func staleCustomListMembers(key string, err error) ([]CustomListMember, error) {
	if stale, ok := customListMembersCache.getStale(key); ok {
		if errors.Is(err, ErrBudgetLow) {
			return stale, nil
		}
		return stale, ErrFromCache
	}
	return nil, err
}

// AsCustomLists is the membership in the CustomLists shape the excluded-list check reads.
func (m CustomListMember) AsCustomLists() CustomLists {
	cl := make(CustomLists, len(m.Lists))
	for _, name := range m.Lists {
		cl[name] = true
	}
	return cl
}
