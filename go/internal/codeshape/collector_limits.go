// SPDX-License-Identifier: MIT OR Apache-2.0

package codeshape

func (limits FunctionLimits) LimitFor(metric Metric) int { return limits.limitFor(metric) }
