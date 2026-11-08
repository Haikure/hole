package core

import "fmt"

// Pool prediction is deliberately a bounded scan, never a cross-socket fixed
// target model. Direction belongs to the calibrated IP, including on retries.
func makePoolPunchPlan(plan *natPunchPlan, model NATSnapshot, width int) (*natPunchPlan, error) {
	if model.Current().Status != "complete" || width < 1 || width > 256 {
		return nil, errNATPunchModel
	}
	if natGroup(model) == "cone" {
		if plan.Sockets != 1 {
			return nil, errNATPunchModel
		}
		return plan, nil
	}
	trend, step := "incremental", 1
	if len(model.IPEvidence) > 1 {
		for _, e := range model.IPEvidence {
			if e.IP != plan.IP {
				continue
			}
			if e.Trend == "irregular" {
				return nil, fmt.Errorf("%w: calibrated IP has irregular samples", errNATPunchModel)
			}
			trend = e.Trend
			if e.Step > 0 && e.Step <= 1024 {
				step = e.Step
			}
			break
		}
	} else {
		// The other endpoint can be an ordinary predictable symmetric NAT.
		if natGroup(model) != "symmetric" || model.PublicIP != "" && model.PublicIP != plan.IP {
			return nil, errNATPunchModel
		}
		trend = model.Predictability
		if trend != "incremental" && trend != "decremental" && trend != "predictable_incremental" && trend != "predictable_decremental" {
			return nil, errNATPunchModel
		}
		if model.PortStep > 0 && model.PortStep <= 1024 {
			step = model.PortStep
		}
	}
	if trend == "stable" {
		return plan, nil
	}
	plan.Target = plan.Port + step
	plan.Width = min(width, 65536-plan.Target)
	if trend == "decremental" || trend == "predictable_decremental" {
		last := plan.Port - step
		plan.Target = max(1, last-width+1)
		plan.Width = last - plan.Target + 1
	}
	if plan.Target < 1 || plan.Target > 65535 || plan.Width < 1 {
		return nil, fmt.Errorf("%w: prediction exceeds port boundary", errNATPunchModel)
	}
	return plan, nil
}
