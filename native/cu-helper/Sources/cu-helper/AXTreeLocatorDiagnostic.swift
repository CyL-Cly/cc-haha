import Foundation

/// Metadata-only explanation of the original SnapshotPathStep rejection. This
/// does not create a usable step, choose a fallback sibling or change IDs.
struct AXLocatorStepDiagnostic: Encodable, Sendable, Equatable {
    let depth: Int
    let selectedIndex: Int
    let siblingCount: Int
    let matchingFingerprintCount: Int
    let matchingTopologyCount: Int?
    let reason: String

    static func rejectedStep(
        depth: Int,
        selectedIndex: Int,
        fingerprints: [ElementFingerprint],
        topologyAt: @escaping (Int) -> [ElementFingerprint]?
    ) -> AXLocatorStepDiagnostic? {
        var topologyCache: [Int: [ElementFingerprint]] = [:]
        var absentTopologies: Set<Int> = []
        func topology(_ index: Int) -> [ElementFingerprint]? {
            if absentTopologies.contains(index) { return nil }
            if let cached = topologyCache[index] { return cached }
            guard let value = topologyAt(index) else {
                absentTopologies.insert(index)
                return nil
            }
            topologyCache[index] = value
            return value
        }
        // Invoke the real selection policy first; the explanation cannot turn a
        // rejected candidate into an action-refetchable path.
        guard SnapshotPathStep(
            selectedIndex: selectedIndex,
            childFingerprints: fingerprints,
            childTopologyAt: topology
        ) == nil else { return nil }
        guard fingerprints.indices.contains(selectedIndex) else {
            return Self(depth: depth, selectedIndex: selectedIndex,
                        siblingCount: fingerprints.count, matchingFingerprintCount: 0,
                        matchingTopologyCount: nil, reason: "selected_child_missing")
        }
        let selected = fingerprints[selectedIndex]
        let matching = fingerprints.indices.filter { selected.matches(fingerprints[$0]) }
        guard let selectedTopology = topology(selectedIndex) else {
            return Self(depth: depth, selectedIndex: selectedIndex,
                        siblingCount: fingerprints.count, matchingFingerprintCount: matching.count,
                        matchingTopologyCount: nil, reason: "selected_topology_unavailable")
        }
        var count = 0
        for index in matching {
            guard let candidate = topology(index) else {
                return Self(depth: depth, selectedIndex: selectedIndex,
                            siblingCount: fingerprints.count, matchingFingerprintCount: matching.count,
                            matchingTopologyCount: nil, reason: "sibling_topology_unavailable")
            }
            if candidate == selectedTopology { count += 1 }
        }
        return Self(depth: depth, selectedIndex: selectedIndex,
                    siblingCount: fingerprints.count, matchingFingerprintCount: matching.count,
                    matchingTopologyCount: count, reason: "duplicate_fingerprint_and_topology")
    }
}

/// This is an internal diagnostic, never an MCP tool result or an action
/// capability. `firstCurrentRejectedStep` describes a fresh read of the tree;
/// a past snapshot did not record the reason its path became nil.
struct AXTreeLocatorDiagnostic: Encodable, Sendable {
    let snapshotID: String
    let elementID: Int
    let rootKind: String
    let rawPath: [Int]
    let storedPathPresent: Bool
    let storedPathLength: Int?
    let snapshotActionRefetchable: Bool
    let reconciliationActionRefetchable: Bool
    let processIdentityMatches: Bool
    let currentRootProven: Bool
    let currentRefetchable: Bool
    let currentResolution: String
    let firstCurrentRejectedStep: AXLocatorStepDiagnostic?
}
