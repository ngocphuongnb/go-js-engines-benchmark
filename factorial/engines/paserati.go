package engines

import "github.com/nooga/paserati/pkg/driver"

type Paserati struct {
	p *driver.Paserati
}

func (p *Paserati) Name() string {
	return "Paserati"
}

func (p *Paserati) Init() error {
	p.p = driver.NewPaserati()
	p.p.SetIgnoreTypeErrors(true) // Run in pure JS mode
	return nil
}

func (p *Paserati) Close() error {
	p.p = nil
	return nil
}

func (p *Paserati) Run(input string) error {
	_, errs := p.p.EvalCode(input, false)
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

var _ JSEngine = (*Paserati)(nil)
