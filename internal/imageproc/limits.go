package imageproc

const (
	// MinimumDimension is the smallest longest-side we will shrink to while chasing a file-size limit.
	MinimumDimension = 64
	// MinJPEGQuality is the floor of the quality search before dimensions are reduced.
	MinJPEGQuality = 40
	// maxResizeAttempts bounds the shrink loop so pathological inputs cannot spin forever.
	maxResizeAttempts = 12
	// Each shrink step scales by at least minShrink and at most maxShrink.
	minShrink = 0.5
	maxShrink = 0.9
)
