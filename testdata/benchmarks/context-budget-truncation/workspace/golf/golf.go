package golf

type Handler struct{}

func New() *Handler { return &Handler{} }

func (h *Handler) Handle(request string) string { return request }
