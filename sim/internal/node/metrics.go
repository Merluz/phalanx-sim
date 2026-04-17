package node

// NodeMetrics is intentionally formula-free in this scaffold.
// It mirrors the slots used by the existing project so you can plug formulas later.
type NodeMetrics struct {
	B, St, L, Y, R, U, T float64

	QRaw    float64
	QSmooth float64
	HR      float64
	Q       float64
}

// Reset clears all metrics.
func (m *NodeMetrics) Reset() { *m = NodeMetrics{} }

