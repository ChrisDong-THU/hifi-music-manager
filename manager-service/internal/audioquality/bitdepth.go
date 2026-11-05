// SPDX-License-Identifier: MPL-2.0
// Adapted from FlacCompagnon core/src/analysis/bitdepth.rs, commit
// 7a382761a2054e07d3572736101e64560ef09057. FlacCompagnon contributors.
package audioquality

import "math/bits"

type grid struct {
	shift, count, rich int
	min, max           int64
	bins               uint64
}

func newGrid(shift int) grid { return grid{shift: shift, min: 1 << 62, max: -1 << 62} }

func (g *grid) push(sample int32) bool {
	step := int64(1) << g.shift
	code := (int64(sample) + step/2) >> g.shift
	residual := int64(sample) - code*step
	if residual < -step/16 || residual > step/16 {
		return false
	}
	g.count++
	g.min, g.max = min(g.min, code), max(g.max, code)
	g.bins |= uint64(1) << uint(code&63)
	if g.count == 4096 {
		if g.max-g.min >= 64 && bits.OnesCount64(g.bins) >= 32 {
			g.rich++
		}
		g.count, g.min, g.max, g.bins = 0, 1<<62, -1<<62, 0
	}
	return true
}

type channelDepth struct {
	mask  uint32
	grids []grid
}

func newDepth(declared int) channelDepth {
	c := channelDepth{}
	for shift := 6; shift <= min(24, declared-8); shift++ {
		c.grids = append(c.grids, newGrid(shift))
	}
	return c
}

func (c *channelDepth) push(sample int32) {
	c.mask |= uint32(sample)
	if c.mask == 0 {
		return
	}
	for i := 0; i < len(c.grids); {
		if c.grids[i].push(sample) {
			i++
			continue
		}
		// Candidates are unordered. Remove an invalid one without copying every
		// surviving grid (including its counters) on every PCM sample.
		last := len(c.grids) - 1
		c.grids[i] = c.grids[last]
		c.grids = c.grids[:last]
	}
}

func effectiveBits(mask uint32, declared int) int {
	if declared < 32 {
		mask &= (uint32(1) << declared) - 1
	}
	if mask == 0 {
		return 1
	}
	return max(1, declared-bits.TrailingZeros32(mask))
}

func (c channelDepth) depth(declared int) int {
	n := effectiveBits(c.mask, declared)
	for _, g := range c.grids {
		if g.rich >= 3 {
			n = min(n, declared-g.shift)
		}
	}
	return n
}
