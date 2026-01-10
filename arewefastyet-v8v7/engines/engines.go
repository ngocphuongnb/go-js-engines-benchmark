package engines

// JSEngine interface that any JS engine must implement
type JSEngine interface {
	Name() string
	Init() error
	Run(input string) ([][]string, error)
	Close() error
}

func Engines() []JSEngine {
	return []JSEngine{
		&GOJA{},
		&ModerncQuickJS{},
		&QJS{},
		&Paserati{},
	}
}
