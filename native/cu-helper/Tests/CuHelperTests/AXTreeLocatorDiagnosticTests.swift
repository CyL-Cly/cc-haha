import XCTest
@testable import cc_haha_computer_use

final class AXTreeLocatorDiagnosticTests: XCTestCase {
    private func fingerprint(_ name: String) -> ElementFingerprint {
        ElementFingerprint(role: "AXGroup", subrole: nil, identifier: name,
                           title: nil, valueKind: nil)
    }

    func testActualStepRejectsAmbiguousAncestorAndDiagnosticNamesExactFirstCause() throws {
        let duplicate = fingerprint("same")
        let leaf = fingerprint("descendant")
        let siblings = [duplicate, duplicate]
        XCTAssertNil(SnapshotPathStep(selectedIndex: 0, childFingerprints: siblings,
                                      childTopologyAt: { _ in [leaf] }))
        let diagnostic = try XCTUnwrap(AXLocatorStepDiagnostic.rejectedStep(
            depth: 3, selectedIndex: 0, fingerprints: siblings, topologyAt: { _ in [leaf] }
        ))
        XCTAssertEqual(diagnostic.reason, "duplicate_fingerprint_and_topology")
        XCTAssertEqual(diagnostic.depth, 3)
        XCTAssertEqual(diagnostic.matchingFingerprintCount, 2)
        XCTAssertEqual(diagnostic.matchingTopologyCount, 2)
    }

    func testUniqueTopologyAndUniqueFingerprintRemainUsableWithoutDiagnostic() {
        let duplicate = fingerprint("same")
        let unique = fingerprint("unique")
        for siblings in [[unique, duplicate], [duplicate, duplicate]] {
            let topologies = [[fingerprint("one")], [fingerprint("two")]]
            XCTAssertNotNil(SnapshotPathStep(selectedIndex: 0, childFingerprints: siblings,
                                             childTopologyAt: { topologies[$0] }))
            XCTAssertNil(AXLocatorStepDiagnostic.rejectedStep(
                depth: 0, selectedIndex: 0, fingerprints: siblings, topologyAt: { topologies[$0] }
            ))
        }
    }

    func testUnavailableChildAndTopologyClassificationsDoNotInventLocators() throws {
        let duplicate = fingerprint("same")
        let rows: [(Int, [ElementFingerprint], (Int) -> [ElementFingerprint]?, String)] = [
            (2, [duplicate], { _ in [] }, "selected_child_missing"),
            (0, [duplicate, duplicate], { _ in nil }, "selected_topology_unavailable"),
            (0, [duplicate, duplicate], { $0 == 0 ? [] : nil }, "sibling_topology_unavailable"),
        ]
        for (index, children, topology, reason) in rows {
            XCTAssertNil(SnapshotPathStep(selectedIndex: index, childFingerprints: children,
                                          childTopologyAt: topology))
            XCTAssertEqual(try XCTUnwrap(AXLocatorStepDiagnostic.rejectedStep(
                depth: 1, selectedIndex: index, fingerprints: children, topologyAt: topology
            )).reason, reason)
        }
    }

    func testDiagnosticIsDaemonOnlyAndNeverAOneShotAuthorizationShortcut() {
        XCTAssertTrue(HelperClientPolicy.isDaemonCommandAllowed("locator_diagnostic"))
        XCTAssertEqual(HelperClientPolicy.authorizeOneShot(
            command: "locator_diagnostic", processChain: [],
            helper: AttestedProcess(pid: 1, parentPID: 0, executablePath: "/private/helper",
                                    identifier: HelperClientPolicy.helperIdentifier,
                                    signatureValid: false)
        ), .deny)
    }
}
