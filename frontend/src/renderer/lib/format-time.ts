import { formatTimeCompact as formatPortableTimeCompact } from "@aoagents/product-ui";
import { appI18n, type MessageKey } from "../i18n";

export function formatTimeCompact(isoDate: string | null | undefined): string {
	return formatPortableTimeCompact(isoDate, {
		translate: (key, values) => appI18n.t(key as MessageKey, values),
	});
}

/**
 * Renders a millisecond duration compactly (e.g. "1h 05m", "42s"). A `null`
 * duration means the value was never measured, so it renders as an explicit
 * dash rather than fabricating "0s" for unmeasured time.
 */
export function formatDurationMs(value: number | null | undefined): string {
	if (value === null || value === undefined || !Number.isFinite(value) || value < 0) return "—";
	const totalSeconds = Math.floor(value / 1000);
	const hours = Math.floor(totalSeconds / 3600);
	const minutes = Math.floor((totalSeconds % 3600) / 60);
	const seconds = totalSeconds % 60;
	if (hours > 0) return `${hours}h ${String(minutes).padStart(2, "0")}m`;
	if (minutes > 0) return `${minutes}m ${String(seconds).padStart(2, "0")}s`;
	return `${seconds}s`;
}

/** Extra-terse relative time for space-constrained navigation rows. */
export function formatTimeTerse(
	isoDate: string | null | undefined,
	now: number | Date = Date.now(),
): string {
	if (!isoDate) return "now";
	const timestamp = new Date(isoDate).getTime();
	if (!Number.isFinite(timestamp)) return "now";
	const nowMs = now instanceof Date ? now.getTime() : now;
	const diffMinutes = Math.floor((nowMs - timestamp) / 60_000);
	if (diffMinutes < 1) return "now";
	if (diffMinutes < 60) return `${diffMinutes}m`;
	const diffHours = Math.floor(diffMinutes / 60);
	if (diffHours < 24) return `${diffHours}h`;
	return `${Math.floor(diffHours / 24)}d`;
}
