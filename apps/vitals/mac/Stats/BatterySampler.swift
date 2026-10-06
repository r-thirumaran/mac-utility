import Foundation
import IOKit.ps

/// The internal battery and the charger, from `IOPSCopyPowerSourcesInfo`.
///
/// `Current Capacity` is already a percentage for internal batteries (`Max Capacity` is
/// 100, but the ratio is taken anyway), `Is Charging` is the charger state and
/// `Time to Empty` is in minutes (−1 while the estimate settles). A Mac without a battery —
/// or with only a UPS — has no `InternalBattery` entry; the reading then says
/// `present == false, onAC == true`, the desktop shape of the other backends. The description
/// dictionary is owned by the snapshot and must not be released separately.
///
/// Safe to call from any one queue at a time.
final class BatterySampler {

    struct Reading {
        var present: Bool
        /// 0 … 100.
        var percent: Int
        var charging: Bool
        var onAC: Bool
        /// Minutes, `nil` when unknown or when plugged in.
        var timeToEmptyMin: Int?
    }

    static let noBattery = Reading(present: false, percent: 0, charging: false, onAC: true, timeToEmptyMin: nil)

    // MARK: - Sampling

    func sample() -> Reading {
        guard let snapshot = IOPSCopyPowerSourcesInfo()?.takeRetainedValue(),
              let sources = IOPSCopyPowerSourcesList(snapshot)?.takeRetainedValue() else {
            return BatterySampler.noBattery
        }

        var onAC = true
        if let providing = IOPSGetProvidingPowerSourceType(snapshot)?.takeUnretainedValue() {
            onAC = (providing as String) == kIOPSACPowerValue
        }

        for source in sources as NSArray {
            guard let description = IOPSGetPowerSourceDescription(snapshot, source as AnyObject)?
                    .takeUnretainedValue() as? [String: Any],
                  description[kIOPSTypeKey] as? String == kIOPSInternalBatteryType else {
                continue
            }
            if let present = description[kIOPSIsPresentKey] as? Bool, !present { continue }

            let charging = description[kIOPSIsChargingKey] as? Bool ?? false
            let batteryOnAC = (description[kIOPSPowerSourceStateKey] as? String).map { $0 == kIOPSACPowerValue } ?? onAC
            var timeToEmpty: Int?
            if !batteryOnAC, let minutes = description[kIOPSTimeToEmptyKey] as? Int, minutes > 0 {
                timeToEmpty = minutes
            }
            return Reading(present: true,
                           percent: BatterySampler.percent(description),
                           charging: charging,
                           onAC: batteryOnAC,
                           timeToEmptyMin: timeToEmpty)
        }

        var reading = BatterySampler.noBattery
        reading.onAC = onAC
        return reading
    }

    // MARK: - Helpers

    private static func percent(_ description: [String: Any]) -> Int {
        let current = description[kIOPSCurrentCapacityKey] as? Int ?? 0
        let maximum = description[kIOPSMaxCapacityKey] as? Int ?? 100
        guard maximum > 0 else { return min(100, max(0, current)) }
        let ratio = Double(current) / Double(maximum) * 100
        return min(100, max(0, Int(ratio.rounded())))
    }
}
