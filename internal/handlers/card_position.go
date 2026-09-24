package handlers

import (
	"fmt"
	"math"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/utils"
)

// Kanban card positioning, shared by Deal (lane = stage) and Lead/Prospect
// (lane = status). See Deal.Position's doc comment (models/deal.go) for the
// scheme itself.

const (
	// maxCardPosition bounds a client-sent position. Real values stay near
	// 1..lane size, so anything this large is a client bug, and rejecting it
	// keeps midpoints far from float64's precision limits.
	maxCardPosition = 1e9
	// cardPositionMinGap is how close two cards in a lane may get before the
	// lane is renumbered. Each drag-move halves the gap it lands in, so
	// without this, ~50 drops into the same spot would exhaust float64 and
	// make neighbors equal. At 1e-6 the midpoints are still exact.
	cardPositionMinGap = 1e-6
	// LaneRebalancedHeader is set to "true" on a move response when the
	// destination lane was renumbered, telling the client its other cards'
	// cached positions are stale and the lane must be refetched.
	LaneRebalancedHeader = "X-Lane-Rebalanced"
)

// cardLanes names one card type's table and lane column. Every query below
// is unscoped (soft-deleted rows included), so a restored card never lands
// on a position already taken in its lane.
type cardLanes struct {
	table   string
	laneCol string
}

var (
	dealLanes     = cardLanes{table: "deals", laneCol: "stage"}
	leadLanes     = cardLanes{table: "leads", laneCol: "status"}
	prospectLanes = cardLanes{table: "prospects", laneCol: "status"}
)

// next returns the position that appends a card to the end of lane:
// MAX(position) + 1, or 1 for an empty lane.
func (l cardLanes) next(db *gorm.DB, lane interface{}) float64 {
	var max float64
	db.Table(l.table).Where(l.laneCol+" = ?", lane).Select("COALESCE(MAX(position), 0)").Scan(&max)
	return max + 1
}

// placeOnMove returns a moved card's new position: the client's requested
// drop point when it sent one (a drag), otherwise the end of the new lane
// if the lane changed (a dropdown-move has no drag geometry), otherwise
// current, unchanged.
func (l cardLanes) placeOnMove(db *gorm.DB, requested *float64, laneChanged bool, newLane interface{}, current float64) float64 {
	switch {
	case requested != nil:
		return *requested
	case laneChanged:
		return l.next(db, newLane)
	default:
		return current
	}
}

// rebalanceIfCrowded renumbers lane to 1..n (keeping its current order, ties
// broken by id) when the card id just saved at *position sits within
// cardPositionMinGap of another card, then reloads *position. Raw SQL, so
// no row's updated_at moves: reordering isn't an edit. Run it in the move's
// transaction, after the card is saved.
func (l cardLanes) rebalanceIfCrowded(tx *gorm.DB, lane interface{}, id uint, position *float64) (bool, error) {
	var crowded int64
	if err := tx.Table(l.table).
		Where(l.laneCol+" = ? AND id <> ? AND ABS(position - ?) < ?", lane, id, *position, cardPositionMinGap).
		Count(&crowded).Error; err != nil {
		return false, err
	}
	if crowded == 0 {
		return false, nil
	}
	sql := fmt.Sprintf(`
		UPDATE %[1]s SET position = sub.rn
		FROM (
			SELECT id, ROW_NUMBER() OVER (ORDER BY position, id) AS rn
			FROM %[1]s WHERE %[2]s = ?
		) sub
		WHERE %[1]s.id = sub.id`, l.table, l.laneCol)
	if err := tx.Exec(sql, lane).Error; err != nil {
		return false, err
	}
	if err := tx.Table(l.table).Where("id = ?", id).Select("position").Scan(position).Error; err != nil {
		return false, err
	}
	return true, nil
}

// validateCardPosition rejects an out-of-range client-sent position, writing
// the 422 itself (callers return nil on error, like the other validate*
// helpers). nil (omitted) is always valid.
func validateCardPosition(c *fiber.Ctx, position *float64) error {
	if position == nil {
		return nil
	}
	if math.IsNaN(*position) || math.Abs(*position) > maxCardPosition {
		_ = utils.ValidationError(c, fmt.Sprintf("position must be between -%g and %g", maxCardPosition, maxCardPosition), map[string][]string{"position": {"out_of_range"}})
		return utils.ErrHandled
	}
	return nil
}
