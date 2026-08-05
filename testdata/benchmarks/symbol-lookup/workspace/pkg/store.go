package pkg

type Store struct {
	items map[string]string
}

func NewStore() *Store {
	return &Store{items: map[string]string{}}
}

func (s *Store) Put(key, value string) {
	s.items[key] = value
}
