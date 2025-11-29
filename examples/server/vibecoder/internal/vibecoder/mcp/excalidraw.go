package mcp

import (
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/examples/server/vibecoder/internal/vibecoder/domain"
	"github.com/modelcontextprotocol/go-sdk/examples/server/vibecoder/internal/vibecoder/graph"
)

// Excalidraw structs
type ExcalidrawDoc struct {
	Type     string               `json:"type"`
	Version  int                  `json:"version"`
	Source   string               `json:"source"`
	Elements []ExcalidrawElement  `json:"elements"`
	AppState ExcalidrawAppState   `json:"appState"`
}

type ExcalidrawElement struct {
	ID              string   `json:"id"`
	Type            string   `json:"type"`
	X               float64  `json:"x"`
	Y               float64  `json:"y"`
	Width           float64  `json:"width"`
	Height          float64  `json:"height"`
	Angle           float64  `json:"angle"`
	StrokeColor     string   `json:"strokeColor"`
	BackgroundColor string   `json:"backgroundColor"`
	FillStyle       string   `json:"fillStyle"`
	StrokeWidth     float64  `json:"strokeWidth"`
	StrokeStyle     string   `json:"strokeStyle"`
	Roughness       float64  `json:"roughness"`
	Opacity         float64  `json:"opacity"`
	GroupID         []string `json:"groupIds"`
	BoundElements   []Binding `json:"boundElements,omitempty"`

	// For Text
	Text           string  `json:"text,omitempty"`
	FontSize       float64 `json:"fontSize,omitempty"`
	FontFamily     int     `json:"fontFamily,omitempty"`
	TextAlign      string  `json:"textAlign,omitempty"`
	VerticalAlign  string  `json:"verticalAlign,omitempty"`

	// For Arrow
	Points      [][]float64 `json:"points,omitempty"`
	StartBinding *Binding   `json:"startBinding,omitempty"`
	EndBinding   *Binding   `json:"endBinding,omitempty"`
	EndArrowhead string     `json:"endArrowhead,omitempty"`
}

type Binding struct {
	ID string `json:"id"` // Element ID
}

type ExcalidrawAppState struct {
	ViewBackgroundColor string `json:"viewBackgroundColor"`
	GridSize            int    `json:"gridSize"`
}

func generateExcalidraw(g *graph.Graph) ([]byte, error) {
	nodes := g.GetAllNodes()
	elements := []ExcalidrawElement{}

	// Layout Logic
	// Columns: Req(0), Feat(1), Domain(2), App(3), Infra(4), Interface(5), Tests(6)

	colWidth := 250.0
	rowHeight := 100.0

	// Group nodes by column
	cols := make(map[int][]*domain.Node)
	nodePos := make(map[string][2]float64) // id -> x, y

	for _, n := range nodes {
		cIndex := 7 // default (Misc)
		switch n.Kind {
		case domain.NodeKindRequirement:
			cIndex = 0
		case domain.NodeKindFeature, domain.NodeKindGherkinFeature, domain.NodeKindGherkinScenario:
			cIndex = 1
		case domain.NodeKindCode:
			layer, _ := n.Metadata["layer"].(string)
			switch layer {
			case "domain": cIndex = 2
			case "application": cIndex = 3
			case "infrastructure": cIndex = 4
			case "interface": cIndex = 5
			default:
				// check if test
				if layer == "" && (n.Kind == domain.NodeKindTest || n.Kind == domain.NodeKindStepDefinition) {
					cIndex = 6
				} else {
					cIndex = 3 // fallback
				}
			}
		case domain.NodeKindStepDefinition:
			cIndex = 6
		}
		cols[cIndex] = append(cols[cIndex], n)
	}

	for col, ns := range cols {
		startX := float64(col) * colWidth
		startY := 100.0

		// Add Column Header
		header := ExcalidrawElement{
			ID: fmt.Sprintf("header-%d", col),
			Type: "text",
			X: startX,
			Y: 50,
			Text: getColName(col),
			FontSize: 20,
			FontFamily: 1,
			StrokeColor: "#000000",
		}
		elements = append(elements, header)

		for i, n := range ns {
			x := startX + 20
			y := startY + float64(i)*rowHeight

			nodePos[n.ID] = [2]float64{x, y}

			bgColor := "#ffffff"
			switch n.Kind {
			case domain.NodeKindRequirement: bgColor = "#ffec99" // yellow
			case domain.NodeKindCode: bgColor = "#d0ebff" // blue
			case domain.NodeKindFeature: bgColor = "#b2f2bb" // green
			case domain.NodeKindGherkinScenario: bgColor = "#b2f2bb"
			case domain.NodeKindStepDefinition: bgColor = "#e599f7" // purple
			}

			// Rectangle
			rectID := "rect-" + n.ID
			rect := ExcalidrawElement{
				ID:              rectID,
				Type:            "rectangle",
				X:               x,
				Y:               y,
				Width:           200,
				Height:          60,
				StrokeColor:     "#000000",
				BackgroundColor: bgColor,
				FillStyle:       "solid",
				StrokeWidth:     1,
				Roughness:       1,
				Opacity:         100,
			}

			// Label
			label := ExcalidrawElement{
				ID:            "text-" + n.ID,
				Type:          "text",
				X:             x + 10,
				Y:             y + 20,
				Width:         180,
				Height:        20,
				Text:          shortID(n.ID),
				FontSize:      14,
				FontFamily:    1,
				StrokeColor:   "#000000",
				TextAlign:     "center",
				VerticalAlign: "middle",
			}

			elements = append(elements, rect, label)
		}
	}

	// Edges
	for _, n := range nodes {
		edges := g.GetEdgesFrom(n.ID)
		startP, ok1 := nodePos[n.ID]
		if !ok1 { continue }

		for _, e := range edges {
			endP, ok2 := nodePos[e.TargetID]
			if !ok2 { continue }

			// Simple straight line (simplified)
			// Excalidraw expects points relative to X,Y? No, points are relative to 0,0 of the arrow?
			// "points": [[0, 0], [dx, dy]] where 0,0 is at X,Y.

			// Start from right of source to left of target?
			// Rect width is 200.

			sx := startP[0] + 200
			sy := startP[1] + 30
			tx := endP[0]
			ty := endP[1] + 30

			arrowX := sx
			arrowY := sy
			dx := tx - sx
			dy := ty - sy

			edgeColor := "#000000"
			if e.Type == domain.EdgeTypeImports {
				edgeColor = "#868e96" // gray
			}

			arrow := ExcalidrawElement{
				ID:           fmt.Sprintf("edge-%s-%s-%s", n.ID, e.TargetID, e.Type),
				Type:         "arrow",
				X:            arrowX,
				Y:            arrowY,
				StrokeColor:  edgeColor,
				StrokeWidth:  1,
				Roughness:    0,
				Points:       [][]float64{{0, 0}, {dx, dy}},
				EndArrowhead: "arrow",
				StartBinding: &Binding{ID: "rect-" + n.ID},
				EndBinding:   &Binding{ID: "rect-" + e.TargetID},
			}
			elements = append(elements, arrow)
		}
	}

	doc := ExcalidrawDoc{
		Type:     "excalidraw",
		Version:  2,
		Source:   "https://vibecoder.com",
		Elements: elements,
		AppState: ExcalidrawAppState{
			ViewBackgroundColor: "#ffffff",
			GridSize:            20,
		},
	}

	return json.MarshalIndent(doc, "", "  ")
}

func getColName(i int) string {
	switch i {
	case 0: return "Requirements"
	case 1: return "Features / BDD"
	case 2: return "Domain"
	case 3: return "Application"
	case 4: return "Infrastructure"
	case 5: return "Interface"
	case 6: return "Tests / Steps"
	default: return "Misc"
	}
}

func shortID(id string) string {
	// Returns last part of path or id
	if len(id) > 25 {
		return "..." + id[len(id)-22:]
	}
	return id
}
