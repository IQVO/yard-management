import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebar: SidebarsConfig = {
  apisidebar: [
    {
      type: "doc",
      id: "api-reference/rest/yard-management-api",
    },
    {
      type: "category",
      label: "Visits",
      link: {
        type: "doc",
        id: "api-reference/rest/visits",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/check-in-visit",
          label: "Check a visit in at the gate (CheckInVisit)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/list-visits",
          label: "List visits",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-visit",
          label: "Get a visit",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/check-out-visit",
          label: "Check a visit out (CheckOutVisit)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/reject-at-gate",
          label: "Turn a visit away at the gate (RejectAtGate)",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "Trailers",
      link: {
        type: "doc",
        id: "api-reference/rest/trailers",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/list-trailers",
          label: "List trailers",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-trailer",
          label: "Get a trailer",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/start-door-work",
          label: "Start door work (StartDoorWork)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/complete-door-work",
          label: "Complete door work (CompleteDoorWork)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/place-hold",
          label: "Place a hold (PlaceHold)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/release-hold",
          label: "Release the hold (ReleaseHold)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/record-seal",
          label: "Record or break the seal (RecordSeal, BreakSeal)",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "Moves",
      link: {
        type: "doc",
        id: "api-reference/rest/moves",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/request-move",
          label: "Request a move (RequestMove)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/list-moves",
          label: "List moves",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/claim-next-move",
          label: "Claim the next move (ClaimNextMove)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/complete-move",
          label: "Complete a move (CompleteMove)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/cancel-move",
          label: "Cancel a move (CancelMove)",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "Yard checks",
      link: {
        type: "doc",
        id: "api-reference/rest/yard-checks",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/open-yard-check",
          label: "Open a yard check (OpenYardCheck)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/record-observation",
          label: "Record an observation (RecordObservation)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/close-yard-check",
          label: "Close a yard check (CloseYardCheck)",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "Yard board",
      link: {
        type: "doc",
        id: "api-reference/rest/yard-board",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/get-yard-board",
          label: "Get the yard board",
          className: "api-method get",
        },
      ],
    },
    {
      type: "category",
      label: "Yard policy",
      link: {
        type: "doc",
        id: "api-reference/rest/yard-policy",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/get-yard-policy",
          label: "Get the yard policy",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/declare-yard-policy",
          label: "Declare the yard policy (DeclareYardPolicy)",
          className: "api-method put",
        },
      ],
    },
    {
      type: "category",
      label: "Sweeps",
      link: {
        type: "doc",
        id: "api-reference/rest/sweeps",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/sweep-dwell",
          label: "Run the dwell sweep (DwellSweep)",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/sweep-move-leases",
          label: "Run the lease-expiry sweep (ExpireLease)",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "Reports",
      link: {
        type: "doc",
        id: "api-reference/rest/reports",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/get-yard-dwell-report",
          label: "Yard dwell report",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-door-utilisation-report",
          label: "Door utilisation report",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-hostler-productivity-report",
          label: "Hostler productivity report",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-turn-time-report",
          label: "Gate-to-gate turn time report",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-detention-exposure-report",
          label: "Detention exposure report",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-exceptions-report",
          label: "Exception rates report",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-reports-freshness",
          label: "Report freshness",
          className: "api-method get",
        },
      ],
    },
    {
      type: "category",
      label: "Health",
      link: {
        type: "doc",
        id: "api-reference/rest/health",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/get-healthz",
          label: "Liveness probe",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-readyz",
          label: "Readiness probe",
          className: "api-method get",
        },
      ],
    },
  ],
};

export default sidebar.apisidebar;
