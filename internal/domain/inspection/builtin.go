package inspection

import (
	"slices"

	"github.com/claudioed/yard-management/internal/domain/trailer"
)

// The ids of the built-in templates (ADR 0015). They are reserved: a custom
// template cannot be published under one of them.
const (
	TractorTrailer17Point = "tractor-trailer-17-point"
	EmptyContainer7Point  = "empty-container-7-point"
	ReeferPreCool         = "reefer-pre-cool-and-sanitation"
)

// builtInDefs holds the checklists in plain words of their own. The areas
// follow the public US CBP C-TPAT checklists (the 17-point tractor and trailer
// inspection and the 7-point container inspection, 8 points for refrigerated
// units) and the loader and receiver duties of the FDA sanitary transportation
// rule (21 CFR 1.908); the sources are named in ADR 0015.
var builtInDefs = []PublishInput{
	{ID: TractorTrailer17Point, Version: 1, Title: "Tractor and trailer 17-point security and agricultural inspection", Items: tractorTrailerItems()},
	{ID: EmptyContainer7Point, Version: 1, Title: "Empty container 7-point inspection (8-point for a refrigerated unit)", Items: emptyContainerItems()},
	{ID: ReeferPreCool, Version: 1, Title: "Reefer pre-cool and sanitation", Items: reeferItems()},
}

func security(code, text string) TemplateItem {
	return TemplateItem{Code: code, Text: text, Required: true, HoldReason: trailer.HoldSecurity}
}

func tractorTrailerItems() []TemplateItem {
	return []TemplateItem{
		security("bumper-tires-rims", "Bumpers, tires and rims are sound and show no sign of tampering or anything hidden in them."),
		security("engine", "The engine compartment holds nothing hidden or unexplained."),
		security("tires", "Each tire is intact, correctly inflated and shows no sign of concealed contents."),
		security("tractor-floor", "The floor of the tractor cab is intact, with no altered panels or hidden compartment."),
		security("fuel-tanks", "Fuel tanks are original and unaltered, with nothing concealed in or behind them."),
		security("cab-interior-sleeper", "Interior cab compartments and the sleeper berth hold nothing unexplained."),
		security("air-tanks", "Air tanks are unmodified and show no sign of tampering."),
		security("drive-shafts", "Drive shafts are unaltered, with nothing attached to them or hidden around them."),
		security("fifth-wheel", "The fifth wheel area, including the natural compartment and the skid plate, is unaltered."),
		security("undercarriage", "The outside and the undercarriage of the trailer show no unexplained attachment, weld or modification."),
		security("doors-outside-inside", "Doors, inside and outside, close fully and their locking mechanisms are reliable."),
		security("trailer-floor", "The trailer floor is intact and not raised or repaired in a way that could hide something."),
		security("side-walls", "Side walls are uniform, with no unusual patch, repair or thickened area."),
		security("front-wall", "The front wall is uniform and unaltered."),
		security("ceiling-roof", "The ceiling and roof are intact, with no sign of alteration."),
		{Code: "refrigeration-unit", Text: "The refrigeration unit and its housing are unaltered and hold nothing unexplained.", Required: true, AllowNotApplicable: true, HoldReason: trailer.HoldSecurity},
		security("exhaust", "The exhaust system is unaltered, with nothing hidden in it."),
		{Code: "pest-free", Text: "No visible pest contamination: no animals, insects or other invertebrates, no animal matter, no plant material or soil.", Required: true, HoldReason: trailer.HoldQuality},
		security("door-hardware", "Door handles, rods, hasps, rivets and brackets are intact and show no sign of tampering."),
	}
}

func emptyContainerItems() []TemplateItem {
	return []TemplateItem{
		security("front-wall", "The front wall is uniform and unaltered."),
		security("left-side", "The left side is uniform, with no unusual patch, repair or thickened area."),
		security("right-side", "The right side is uniform, with no unusual patch, repair or thickened area."),
		security("floor", "The inside floor is intact and not raised or repaired in a way that could hide something."),
		security("ceiling-roof", "The ceiling and roof are intact, with no sign of alteration."),
		security("doors-and-locking", "Doors, inside and outside, are sound and their locking mechanisms are reliable."),
		security("outside-undercarriage", "The outside and the undercarriage show no unexplained attachment, weld or modification."),
		{Code: "fan-housing", Text: "On a refrigerated unit, the fan housing is unaltered and holds nothing unexplained.", Required: true, AllowNotApplicable: true, HoldReason: trailer.HoldSecurity},
		{Code: "pest-free", Text: "No visible pest contamination: no animals, insects or other invertebrates, no animal matter, no plant material or soil.", Required: true, HoldReason: trailer.HoldQuality},
		security("door-hardware", "Door handles, rods, hasps, rivets and brackets are intact and show no sign of tampering."),
	}
}

func reeferItems() []TemplateItem {
	q := func(code, text string, na bool) TemplateItem {
		return TemplateItem{Code: code, Text: text, Required: true, AllowNotApplicable: na, HoldReason: trailer.HoldQuality}
	}
	return []TemplateItem{
		{Code: "physical-condition", Text: "The cargo compartment is in adequate physical condition: no damage or gap that could let contamination in.", Required: true, HoldReason: trailer.HoldDamaged},
		q("sanitary-condition-washout", "The compartment is clean and in sanitary condition, washed out where the shipper's cleaning specification asks for it.", false),
		q("no-previous-cargo-residue", "No visible evidence of a previous load that could cause the food to become unsafe.", false),
		q("no-pest-infestation", "No visible sign of pest infestation in the compartment.", false),
		q("pre-cooled-to-set-temperature", "The mechanically refrigerated compartment is pre-cooled to the operating temperature the shipper specified (not applicable to an ambient load or to unloading).", true),
		q("receiver-temperature-abuse-check", "On receipt: the food temperature, the vehicle's ambient temperature and its set temperature were checked, and a sensory check found no sign of temperature abuse (not applicable to a load going out).", true),
	}
}

// BuiltIns returns the templates that ship with the service, in a stable
// order. They are read-only and have no publication time.
func BuiltIns() []*Template {
	out := make([]*Template, 0, len(builtInDefs))
	for _, d := range builtInDefs {
		t, err := build(d, true)
		if err != nil {
			panic("inspection: invalid built-in template " + d.ID + ": " + err.Error())
		}
		out = append(out, t)
	}
	return out
}

// IsBuiltInID reports whether id is the id of a built-in template.
func IsBuiltInID(id string) bool {
	return slices.ContainsFunc(builtInDefs, func(d PublishInput) bool { return d.ID == id })
}
