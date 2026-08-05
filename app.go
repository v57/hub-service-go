package hubservice

// App is a hub app: a header plus a UI body of elements (TS app.ts parity).
// Body is []any — pass plain strings (shorthand text) or element maps built
// with the helpers below.
type App struct {
	Header AppHeader
	Body   []any
	Top    any
	Bottom any
	Data   map[string]any
}

// Action references an API call from a UI element.
type Action struct {
	Path   string `json:"path"`
	Body   any    `json:"body,omitempty"`
	Output any    `json:"output,omitempty"`
}

// Element helpers produce element maps matching the TS app.ts wire shapes.

func Text(value string) map[string]any {
	return map[string]any{"type": "text", "value": value}
}

func Progress(value string, min, max any) map[string]any {
	e := map[string]any{"type": "progress", "value": value}
	if min != nil {
		e["min"] = min
	}
	if max != nil {
		e["max"] = max
	}
	return e
}

func TextField(value, placeholder string, action *Action) map[string]any {
	e := map[string]any{"type": "textField", "value": value}
	if placeholder != "" {
		e["placeholder"] = placeholder
	}
	if action != nil {
		e["action"] = action
	}
	return e
}

func Button(title string, action Action) map[string]any {
	return map[string]any{"type": "button", "title": title, "action": action}
}

func Slider(value string, defaultValue, min, max, step any, action *Action) map[string]any {
	e := map[string]any{"type": "slider", "value": value}
	if defaultValue != nil {
		e["defaultValue"] = defaultValue
	}
	if min != nil {
		e["min"] = min
	}
	if max != nil {
		e["max"] = max
	}
	if step != nil {
		e["step"] = step
	}
	if action != nil {
		e["action"] = action
	}
	return e
}

func List(data string, content any) map[string]any {
	return map[string]any{"type": "list", "data": data, "content": content}
}

func Picker(options []string, selected string) map[string]any {
	return map[string]any{"type": "picker", "options": options, "selected": selected}
}

func Cell(title, subtitle any) map[string]any {
	e := map[string]any{"type": "cell"}
	if title != nil {
		e["title"] = title
	}
	if subtitle != nil {
		e["subtitle"] = subtitle
	}
	return e
}

func Files(value string, title any, action Action) map[string]any {
	e := map[string]any{"type": "files", "value": value, "action": action}
	if title != nil {
		e["title"] = title
	}
	return e
}

func FileOperation(format string, title any, action Action) map[string]any {
	e := map[string]any{"type": "fileOperation", "action": action}
	if format != "" {
		e["format"] = format
	}
	if title != nil {
		e["title"] = title
	}
	return e
}

func HStack(content []any, spacing any) map[string]any {
	e := map[string]any{"type": "hstack", "content": content}
	if spacing != nil {
		e["spacing"] = spacing
	}
	return e
}

func VStack(content []any, spacing any) map[string]any {
	e := map[string]any{"type": "vstack", "content": content}
	if spacing != nil {
		e["spacing"] = spacing
	}
	return e
}

func ZStack(content []any) map[string]any {
	return map[string]any{"type": "zstack", "content": content}
}

func Spacer() map[string]any {
	return map[string]any{"type": "spacer"}
}
