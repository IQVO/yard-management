@bdd
Feature: Standard identifiers at the gate
  The gate speaks the identifiers partners and customs speak. A container number follows ISO 6346 and its check digit is
  verified, so a mistyped number is stopped at the gate. A seal may carry its ISO 17712 class (I, S or H) and a site may
  demand a minimum class. The seal state says whether the seal was only recorded or also checked against the expected one.
  Domestic road trailers keep their free-form numbers, and a request without the new fields behaves exactly as before.

  Background:
    Given a yard site "WH1" with spots "WH1-Y01,WH1-Y02,WH1-Y03" and doors "WH1-D01:Inbound,WH1-D02:Both"

  Scenario: A container with a valid ISO 6346 number is accepted
    When the gate checks in visit "A" with the body {"siteId":"WH1","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"ABC1D23","purpose":"DROP","trailers":[{"unitNumber":"CSQU3054383","kind":"CONTAINER","loadState":"LOADED"}]}
    Then the response status is 201
    And the response field "trailers.0.unitNumber" is "CSQU3054383"
    And the response field "trailers.0.unitNumberScheme" is "ISO6346"
    And the event "VisitCheckedIn" was published
    And the latest "VisitCheckedIn" event has field "trailers.0.unit_number_scheme" "ISO6346"

  Scenario: A container number whose check digit is 0 because the remainder is 10 is accepted
    When the gate checks in visit "A" with the body {"siteId":"WH1","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"ABC1D23","purpose":"DROP","trailers":[{"unitNumber":"ABCU0000070","kind":"CONTAINER","loadState":"EMPTY"}]}
    Then the response status is 201
    And the response field "trailers.0.unitNumberScheme" is "ISO6346"

  Scenario Outline: A container with a bad number is refused at the gate and nothing is recorded
    When the gate checks in visit "A" with the body {"siteId":"WH1","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"ABC1D23","purpose":"DROP","trailers":[{"unitNumber":"<unit>","kind":"CONTAINER","loadState":"LOADED"}]}
    Then the response status is 422
    And the problem is "invalid-unit-number"
    And the response field "detail" contains "<detail>"
    And no "VisitCheckedIn" event was published
    When I send GET "/trailers"
    Then the response list "items" has 0 items

    Examples:
      | unit         | detail                         |
      | CSQU3054380  | expected check digit is 3      |
      | CSQU3054384  | expected check digit is 3      |
      | ABCU0000071  | expected check digit is 0      |
      | CSQU305438   | no check digit                 |
      | csqu3054383  | 3-letter owner code            |
      | CSQX3054383  | category letter U, J or Z      |
      | TR-1042      | 6 digits and a check digit     |

  Scenario Outline: A domestic road trailer keeps its free-form number
    When the gate checks in visit "A" with the body {"siteId":"WH1","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"ABC1D23","purpose":"DROP","trailers":[{"unitNumber":"<unit>","kind":"<kind>","loadState":"LOADED"}]}
    Then the response status is 201
    And the response field "trailers.0.unitNumber" is "<unit>"
    And the response field "trailers.0.unitNumberScheme" is null

    Examples:
      | unit        | kind    |
      | TR-1042     | DRY_VAN |
      | tr 7/b      | REEFER  |
      | 53 FOOT 12  | FLATBED |

  Scenario: A caller can declare ISO 6346 for a trailer that is not a container
    When the gate checks in visit "A" with the body {"siteId":"WH1","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"ABC1D23","purpose":"DROP","trailers":[{"unitNumber":"TR-1042","kind":"DRY_VAN","loadState":"LOADED","unitNumberScheme":"ISO6346"}]}
    Then the response status is 422
    And the problem is "invalid-unit-number"
    When the gate checks in visit "A" with the body {"siteId":"WH1","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"ABC1D23","purpose":"DROP","trailers":[{"unitNumber":"MSCU0000007","kind":"DRY_VAN","loadState":"LOADED","unitNumberScheme":"ISO6346"}]}
    Then the response status is 201
    And the response field "trailers.0.unitNumberScheme" is "ISO6346"

  Scenario: An unknown unit number scheme is a validation failure
    When the gate checks in visit "A" with the body {"siteId":"WH1","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"ABC1D23","purpose":"DROP","trailers":[{"unitNumber":"TR-1042","kind":"DRY_VAN","loadState":"LOADED","unitNumberScheme":"GS1"}]}
    Then the response status is 422
    And the problem is "validation"

  Scenario: A seal may carry its ISO 17712 class
    When the gate checks in visit "A" with the body {"siteId":"WH1","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"ABC1D23","purpose":"DROP","trailers":[{"unitNumber":"TR-1","kind":"DRY_VAN","loadState":"LOADED","sealNumber":"SL-1","sealClass":"H"}]}
    Then the response status is 201
    And the response field "trailers.0.seal.number" is "SL-1"
    And the response field "trailers.0.seal.class" is "H"
    And the response field "trailers.0.seal.state" is "INTACT"
    And trailer "TR-1" has seal class H
    And the latest "VisitCheckedIn" event has field "trailers.0.seal_class" "H"

  Scenario Outline: An unknown seal class, or a class without a seal, is refused
    When the gate checks in visit "A" with the body {"siteId":"WH1","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"ABC1D23","purpose":"DROP","trailers":[{"unitNumber":"TR-1","kind":"DRY_VAN","loadState":"LOADED",<seal>}]}
    Then the response status is 422
    And the problem is "<problem>"
    And no "VisitCheckedIn" event was published

    Examples:
      | seal                                 | problem            |
      | "sealNumber":"SL-1","sealClass":"X"  | invalid-seal-class |
      | "sealNumber":"SL-1","sealClass":"h"  | invalid-seal-class |
      | "sealClass":"S"                      | validation         |

  Scenario: The site yard policy can demand a minimum seal class at check-in
    Given the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"minimumSealClass":"H"}
    When the gate checks in visit "A" with the body {"siteId":"WH1","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"ABC1D23","purpose":"DROP","trailers":[{"unitNumber":"TR-1","kind":"DRY_VAN","loadState":"LOADED","sealNumber":"SL-1","sealClass":"I"}]}
    Then the response status is 409
    And the problem is "seal-class-too-low"
    And no "VisitCheckedIn" event was published
    When I send GET "/trailers"
    Then the response list "items" has 0 items
    When the gate checks in visit "A" with the body {"siteId":"WH1","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"ABC1D23","purpose":"DROP","trailers":[{"unitNumber":"TR-1","kind":"DRY_VAN","loadState":"LOADED","sealNumber":"SL-1","sealClass":"S"}]}
    Then the response status is 409
    And the problem is "seal-class-too-low"
    When the gate checks in visit "A" with the body {"siteId":"WH1","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"ABC1D23","purpose":"DROP","trailers":[{"unitNumber":"TR-1","kind":"DRY_VAN","loadState":"LOADED","sealNumber":"SL-1"}]}
    Then the response status is 409
    And the problem is "seal-class-too-low"
    When the gate checks in visit "A" with the body {"siteId":"WH1","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"ABC1D23","purpose":"DROP","trailers":[{"unitNumber":"TR-1","kind":"DRY_VAN","loadState":"LOADED","sealNumber":"SL-1","sealClass":"H"}]}
    Then the response status is 201
    And trailer "TR-1" has seal class H

  Scenario: A trailer that arrives with no seal is not judged by the seal class rule
    Given the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"minimumSealClass":"H"}
    When the gate checks in visit "A" for purpose DROP with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED)
    Then the response status is 201

  Scenario: Recording a seal of a lower class is refused when the policy demands a higher one
    Given trailer "TR-1" is parked
    And the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"minimumSealClass":"S"}
    When I send POST "/trailers/<trailer TR-1>/seals" with {"state":"INTACT","sealNumber":"SL-5","sealClass":"I"}
    Then the response status is 409
    And the problem is "seal-class-too-low"
    And trailer "TR-1" has seal state NONE
    When I send POST "/trailers/<trailer TR-1>/seals" with {"state":"INTACT","sealNumber":"SL-5"}
    Then the response status is 409
    And the problem is "seal-class-too-low"
    When I send POST "/trailers/<trailer TR-1>/seals" with {"state":"INTACT","sealNumber":"SL-5","sealClass":"Q"}
    Then the response status is 422
    And the problem is "invalid-seal-class"
    When I send POST "/trailers/<trailer TR-1>/seals" with {"state":"APPLIED","sealNumber":"SL-5","sealClass":"H"}
    Then the response status is 200
    And the response field "seal.state" is "APPLIED"
    And the response field "seal.class" is "H"
    And the response field "seal.number" is "SL-5"

  Scenario: The seal states a caller may record
    Given trailer "TR-1" is parked
    When I send POST "/trailers/<trailer TR-1>/seals" with {"state":"VERIFIED","sealNumber":"SL-5"}
    Then the response status is 422
    And the problem is "validation"
    When I send POST "/trailers/<trailer TR-1>/seals" with {"state":"BROKEN","sealClass":"H"}
    Then the response status is 422
    And the problem is "validation"

  Scenario: The minimum seal class is part of the yard policy
    When I send GET "/sites/WH1/yard-policy"
    Then the response field "minimumSealClass" is null
    Given the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"minimumSealClass":"H"}
    When I send GET "/sites/WH1/yard-policy"
    Then the response field "minimumSealClass" is "H"
    When I send PUT "/sites/WH1/yard-policy" with {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300,"minimumSealClass":"X"}
    Then the response status is 422
    And the problem is "invalid-seal-class"
    Given the yard policy of site "WH1" is {"requireAppointment":false,"sealMismatchAction":"FLAG","leaseSeconds":300}
    When I send GET "/sites/WH1/yard-policy"
    Then the response field "minimumSealClass" is null

  Scenario: A seal that matches the expected one is verified at the gate
    Given appointment "appt-1" is booked at door "WH1-D01" for ASN "ASN-1" starting in 10 minutes with expected seal "SL-1"
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1" sealed "SL-1"
    Then the response status is 201
    And the response field "trailers.0.seal.state" is "VERIFIED"
    And trailer "TR-1" has seal state VERIFIED
    And no "SealExceptionRaised" event was published

  Scenario: A seal that differs from the expected one is a mismatch, and the exception is raised as before
    Given appointment "appt-1" is booked at door "WH1-D01" for ASN "ASN-1" starting in 10 minutes with expected seal "SL-1"
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1" sealed "SL-9"
    Then the response status is 201
    And the response field "trailers.0.seal.state" is "MISMATCH"
    And trailer "TR-1" has seal state MISMATCH
    And the event "SealExceptionRaised" was published 1 time
    And the latest "SealExceptionRaised" event has field "expected_seal_number" "SL-1"
    And the latest "SealExceptionRaised" event has field "found_seal_number" "SL-9"
    And the latest "SealExceptionRaised" event has field "appointment_id" "appt-1"

  Scenario: A seal with nothing to compare it with stays intact
    When the gate checks in visit "A" for purpose DROP with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) sealed "SL-1"
    Then the response status is 201
    And trailer "TR-1" has seal state INTACT

  Scenario: A verified seal can still be found broken
    Given appointment "appt-1" is booked at door "WH1-D01" for ASN "ASN-1" starting in 10 minutes with expected seal "SL-1"
    When the gate checks in visit "A" for purpose LIVE_UNLOAD with plate "ABC1D23" and trailer "TR-1" (DRY_VAN, LOADED) on appointment "appt-1" sealed "SL-1"
    And I send POST "/trailers/<trailer TR-1>/seals" with {"state":"BROKEN"}
    Then the response status is 200
    And the response field "seal.state" is "BROKEN"
    And the response field "hold" is "SEAL_BROKEN"

  Scenario: The docking and departure events carry the seal state and class
    Given appointment "appt-1" is booked at door "WH1-D01" for ASN "ASN-1" starting in 10 minutes with expected seal "SL-1"
    When the gate checks in visit "A" with the body {"siteId":"WH1","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"ABC1D23","purpose":"LIVE_UNLOAD","appointmentId":"appt-1","trailers":[{"unitNumber":"TR-1","kind":"DRY_VAN","loadState":"LOADED","sealNumber":"SL-1","sealClass":"S"}]}
    And the gate clerk completes the move of trailer "TR-1"
    Then the latest "TrailerDocked" event has field "seal_state" "VERIFIED"
    And the latest "TrailerDocked" event has field "seal_class" "S"
    When the dock operator starts door work on trailer "TR-1"
    And the dock operator completes door work on trailer "TR-1"
    And visit "A" checks out taking trailers "TR-1"
    Then the latest "TrailerDeparted" event has field "seal_state" "VERIFIED"
    And the latest "TrailerDeparted" event has field "seal_class" "S"
    And the latest "TrailerDeparted" event has field "seal_number" "SL-1"

  Scenario: A request without the new fields behaves exactly as before
    When the gate checks in visit "A" with the body {"siteId":"WH1","carrier":"Acme Freight","carrierScac":"ACME","tractorPlate":"ABC1D23","purpose":"LIVE_UNLOAD","trailers":[{"unitNumber":"TR-1","kind":"DRY_VAN","loadState":"LOADED","sealNumber":"SL-1"}]}
    Then the response status is 201
    And the response field "trailers.0.seal.number" is "SL-1"
    And the response field "trailers.0.seal.state" is "INTACT"
    And the response field "trailers.0.seal.class" is null
    And the response field "trailers.0.unitNumberScheme" is null
    When I send POST "/trailers/<trailer TR-1>/seals" with {"state":"INTACT","sealNumber":"SL-2"}
    Then the response status is 200
    And the response field "seal.state" is "INTACT"
    And the response field "seal.number" is "SL-2"
    And the response field "seal.class" is null
    When I send POST "/trailers/<trailer TR-1>/seals" with {"state":"BROKEN"}
    Then the response status is 200
    And the response field "seal.state" is "BROKEN"
    And the response field "hold" is "SEAL_BROKEN"
