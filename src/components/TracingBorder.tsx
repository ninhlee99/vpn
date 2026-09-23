import React from 'react';

export type TraceTone = 'amber' | 'green' | 'red';

const TONES: Record<TraceTone, { color: string; highlight: string }> = {
  amber: { color: '#f59e0b', highlight: '#fde68a' },
  green: { color: '#10b981', highlight: '#a7f3d0' },
  red: { color: '#ef4444', highlight: '#fecaca' },
};

const TAIL_STEPS = 8;

interface TracingBorderProps {
  tone: TraceTone;
  /** Border-box corner radius of the element this sits on. */
  radius: number;
  /** Seconds per lap. */
  period?: number;
  strokeWidth?: number;
  /** Tail length as a fraction of the perimeter. */
  tail?: number;
}

/**
 * A comet of light travelling around the parent's rounded border.
 *
 * Replaces the old spinning `conic-gradient` div: rotating a gradient over a wide card
 * made the light race along the long edges and crawl round the short ones, and the
 * oversized rotating layer leaked a visible wedge through the card's translucent fill.
 * Here each layer is a dash on an SVG outline normalised with pathLength=100, so the
 * light moves at constant speed along the perimeter whatever the card's aspect ratio.
 *
 * The parent must be `position: relative` with a 1px border.
 */
export const TracingBorder: React.FC<TracingBorderProps> = ({
  tone,
  radius,
  period = 2,
  strokeWidth = 1.6,
  tail = 0.3,
}) => {
  const { color, highlight } = TONES[tone];
  const inset = strokeWidth / 2;
  const rectStyle: React.CSSProperties = {
    x: inset,
    y: inset,
    width: `calc(100% - ${strokeWidth}px)`,
    height: `calc(100% - ${strokeWidth}px)`,
    rx: Math.max(radius - inset, 0),
    animationDuration: `${period}s`,
  } as React.CSSProperties;

  // "0 gap len 0": an empty dash, a gap, then the visible dash ending exactly at the
  // pattern's end. Every layer therefore shares the same head position and the same
  // 0 → -100 dashoffset animation, so they stay in lockstep without per-layer keyframes.
  const dash = (len: number) => `0 ${100 - len} ${len} 0`;

  return (
    <svg
      aria-hidden
      className="absolute -inset-px pointer-events-none overflow-visible"
      style={{ width: 'calc(100% + 2px)', height: 'calc(100% + 2px)' }}
    >
      {Array.from({ length: TAIL_STEPS }, (_, i) => {
        const len = (tail * 100 * (TAIL_STEPS - i)) / TAIL_STEPS;
        return (
          <rect
            key={i}
            pathLength={100}
            fill="none"
            stroke={color}
            strokeOpacity={0.28}
            strokeWidth={strokeWidth}
            strokeDasharray={dash(len)}
            className="trace-dash"
            style={rectStyle}
          />
        );
      })}
      <rect
        pathLength={100}
        fill="none"
        stroke={highlight}
        strokeWidth={strokeWidth + 0.4}
        strokeLinecap="round"
        strokeDasharray={dash(4)}
        className="trace-dash"
        style={{ ...rectStyle, filter: `drop-shadow(0 0 3px ${color})` }}
      />
    </svg>
  );
};
