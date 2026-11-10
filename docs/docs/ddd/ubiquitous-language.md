---
id: ubiquitous-language
title: Ubiquitous Language
sidebar_label: Ubiquitous Language
description: "The terms Yard Management speaks (yard, yard visit, trailer, spot, dock door, hostler, yard move, door work, dwell, seal) and the neighbour terms it uses, each with its Go identifier in the built service."
---

# Ubiquitous Language


The yard-wide glossary is on
[Ubiquitous Language](https://iqvo.github.io/warehouse-docs/yard/strategic-design/ubiquitous-language). Code
identifiers are the Go names found in `internal/domain` of the built service (`package.Type`); `-` marks a term that
names no single type.

## This context's terms

From `contexts[yard-management].ul`.

| Term | Definition | Code identifier | Not to be confused with |
| --- | --- | --- | --- |
| **Yard** | The enclosed area outside a site's four walls: gate, parking spots and dock doors. | `-` | **Warehouse floor** (inside the walls, owned by the other fleet contexts) |
| **Yard Visit** | One tractor and driver passing the gate, from check-in to check-out. | `yardvisit.Visit` | **Dock Appointment** (`dock-scheduling`: a booked window; a visit may or may not name one) |
| **Trailer** | Any towable unit on site, including a container on a chassis (SAP calls it a transportation unit). | `trailer.Trailer` | **Tractor** (the powered unit; only its plate is held, on the visit) |
| **Spot** | A Yard location (`facility-layout` role `Yard`) a trailer parks on. | `shared.Location` | **Dock Door** |
| **Holding Spot** | A spot for walk-ins and early arrivals, named in the yard policy. | `yardpolicy.Policy` | **Spot** in general (a holding spot is one the policy lists in `holding_spot_codes`) |
| **Dock Door** | A Dock location (`facility-layout` role `Dock`) with a `dockFlow` of Inbound, Outbound or Both. | `shared.Location` | **Door Window** (`dock-scheduling`: time on a door, not the door) |
| **Live Load / Live Unload** | The tractor waits at the door while the trailer is worked. | `shared.Purpose` | **Drop** |
| **Drop** | The trailer is left in the yard and the tractor leaves without it. | `shared.Purpose` | **Live Unload** |
| **Bobtail** | A tractor without a trailer. | `shared.Purpose` | **Drop** (a drop leaves a trailer behind; a bobtail brings none) |
| **Hostler** | Yard driver (also jockey, spotter, shunter) who moves trailers with a yard truck. | `yardmove.Performer` | **Visiting Driver** (the carrier's driver; moves only the first move from the gate) |
| **Yard Move** | An instruction to take one trailer to a spot or a door. | `yardmove.Move` | **Dock Appointment** (a booking, not an instruction to move) |
| **Door Work** | Unloading or loading while a trailer is docked; `WAITING`, `IN_PROGRESS`, `DONE`. | `trailer.DoorWork` | **Receipt** (`inbound-receiving`: the receiving of an ASN's goods) |
| **Dwell** | Time on site from gate-in to gate-out (per trailer: arrival to departure). | `trailer.Trailer` | **Detention** |
| **Seal** | The numbered seal on a trailer's doors; `INTACT`, `BROKEN` or `NONE`. | `trailer.SealState` | **Expected seal** (`expected_seal_number` on the appointment: what the carrier declared) |
| **Yard Check** | A yard audit reconciling observed trailers with the model. | `yardcheck.Check` | — |
| **Detention** | Carrier waiting time charged beyond the agreed free time; the free time is a site setting. | `yardpolicy.Policy` | **Dwell** (a duration; detention is the chargeable part beyond free time) |

There is no move to the gate: `VisitingDriverMove` says so, and a `Location` is only a `SPOT` or a `DOOR`. A
departing trailer leaves from a spot or, for a live visit, from the door. This pack's earlier wording (gap
YM-G2) is settled by decision Y-1.

## Value objects and states used on these pages

From the `value_objects` and `states` of the aggregates.

| Term | Definition | Code identifier |
| --- | --- | --- |
| **Visit Purpose** | `DROP`, `LIVE_UNLOAD`, `LIVE_LOAD`, `PICKUP` or `BOBTAIL`. | `shared.Purpose` |
| **Tractor Plate** | The plate of the tractor; at most one `CHECKED_IN` visit per plate per site. | field of `yardvisit.Visit` |
| **Driver Ref** | An opaque reference to the driver; no personal data is stored. | field of `yardvisit.Visit` |
| **Trailer Kind** | `DRY_VAN`, `REEFER`, `FLATBED` or `CONTAINER`. | `shared.TrailerKind` |
| **Unit Number** | `carrier_scac` plus `number`; the natural key of a trailer on site. | fields of `trailer.Trailer` |
| **Load State** | `EMPTY`, `LOADED` or `PARTIAL`. | `shared.LoadState` |
| **Contents** | The `asn_numbers` an inbound trailer holds, or the `outbound_ref` of an outbound one. | fields of `trailer.Trailer` |
| **Hold** | A reason a trailer must not be docked: `DAMAGED`, `SEAL_BROKEN`, `NOT_FOUND`, `QUALITY` or `SECURITY`. | `trailer.HoldReason` |
| **Arrived At** | The instant a trailer reached the site; the dwell clock start. | `trailer.Trailer` |
| **Performer** | Who does a move: `HOSTLER` or `VISITING_DRIVER`. | `yardmove.Performer` |
| **Move Reason** | `TO_DOOR`, `FROM_DOOR`, `TO_SPOT`, `RELOCATE` or `TO_HOLDING`. | `yardmove.Reason` |
| **Priority** | A band 1..9 and a `due_by` time. | `yardmove.Priority` |
| **Lease** | The `hostler_ref` and `expires_at` of a claimed move. | field of `yardmove.Move` |
| **Observation** | A `location_code` and optional `unit_number` recorded in a yard check. | `yardcheck.Observation` |
| **Discrepancy** | `MISSING`, `UNEXPECTED` or `WRONG_LOCATION`, with the location and the trailer. | `yardcheck.Discrepancy` |
| **Rejection Reason** | `APPOINTMENT_REQUIRED`, `UNKNOWN_APPOINTMENT`, `SEAL_MISMATCH`, `TRAILER_ALREADY_ON_SITE` or `SITE_REQUEST`. | `yardvisit.RejectionReason` |
| **Seal Mismatch Action** | `FLAG` or `REJECT`: site policy for a seal that differs from the expected one. | `yardpolicy.SealAction` |
| **Free Time** | The detention clock in minutes; optional, with no default. | `yardpolicy.Policy` |

## Neighbour terms used here

| Term | Definition (owner) | Code identifier | Not to be confused with |
| --- | --- | --- | --- |
| **Dock Appointment** | A booked window on one door for one carrier visit, inbound or outbound (`dock-scheduling`). | `dockappointment.DockAppointment` | **Yard Visit** |
| **Door Window** | The half-open interval `[start, end)` an appointment holds on its door (`dock-scheduling`). | `dockappointment.Window` | **Door Work** |
| **Door Calendar** | A site's door opening hours, closures and booking settings (`dock-scheduling`). | `dockcalendar.DockCalendar` | **Yard Policy** |
| **Closure** | A period a door cannot be booked (maintenance, damage); here it also means the door takes no trailer (`dock-scheduling`). | `dockcalendar.Closure` | **Hold** (on a trailer, not a door) |
| **Arrival Status** | `EARLY`, `ON_TIME` or `LATE`, computed by `dock-scheduling` from the gate arrival (`dock-scheduling`). | `dockappointment.ArrivalStatus` | **Visit Purpose** |
| **No-Show** | A booked appointment whose carrier did not arrive by window end plus grace (`dock-scheduling`). | `dockappointment.NoShow` | **Rejection** (a visit turned away at the gate) |
| **SCAC** | Standard Carrier Alpha Code identifying the carrier. | `shared.Carrier` | **Unit Number** |
| **CPT** | Critical pull time: the fleet's outbound cutoff (`process-path-management`). | consumed by `dock-scheduling`, not here | — |
| **Location Slot** | A registered place in a site; role `Yard` or `Dock`, with `dockFlow` for a dock (`facility-layout`). | `LocationSlotRegistered` | **Spot** and **Dock Door** (the slot is the registry entry) |
| **Zone** | A named area; `ZoneRegistered` maps `zoneId` to `siteCode` (`facility-layout`). | `ZoneRegistered` | **Parking zone** (a zone the yard policy lists in `parking_zone_ids`) |
| **Site Code** | `facility-layout`'s `siteCode`; the `site_id` on every yard event. | `site_id` | A location code (never parsed to find the site) |
| **ASN** | Advance shipping notice (`inbound-receiving`); `ASNRegistered` tells what an inbound trailer may hold. | `asn_number` | **Receipt** |
| **Receipt** | The act of receiving goods at a door; `ReceiptOpened` and `ReceiptClosed` (`inbound-receiving`). | `receipt_id` | **Door Work** (the yard's view of the same time at the door) |

No translation table with the retail network: this context does not touch it
(`meta.hard_boundary`).
