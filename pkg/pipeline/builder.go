package pipeline

type Builder[S any] struct {
	steps []Step[S]
	opts  RunOptions[S]
}

func NewBuilder[S any]() *Builder[S] {
	return &Builder[S]{
		opts: RunOptions[S]{
			StopOnError:  true,
			TrackTimings: true,
		},
	}
}

func (b *Builder[S]) WithOptions(opts RunOptions[S]) *Builder[S] {
	if b == nil {
		return nil
	}
	b.opts = opts
	return b
}

func (b *Builder[S]) Add(step Step[S]) *Builder[S] {
	if b == nil {
		return nil
	}
	b.steps = append(b.steps, step)
	return b
}

func (b *Builder[S]) AddMany(steps ...Step[S]) *Builder[S] {
	if b == nil {
		return nil
	}
	b.steps = append(b.steps, steps...)
	return b
}

func (b *Builder[S]) Build() *Pipeline[S] {
	if b == nil {
		return nil
	}
	p := New[S](b.steps...)
	return p.WithOptions(b.opts)
}

