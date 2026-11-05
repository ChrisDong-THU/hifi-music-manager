import test from "node:test";
import assert from "node:assert/strict";
import {
  createStrokeMotion,
  iconExitRetention,
  iconMotion,
} from "../src/stroke-motion.ts";

function fixture(lengths = [20, 0.1]) {
  const animations = [];
  const elements = lengths.map((length) => {
    const values = new Map();
    const priorities = new Map();
    return {
      computed: {},
      measurements: 0,
      style: {
        getPropertyValue: (key) => values.get(key) || "",
        getPropertyPriority: (key) => priorities.get(key) || "",
        setProperty(key, value, priority = "") {
          values.set(key, value);
          priorities.set(key, priority);
        },
        removeProperty(key) {
          values.delete(key);
          priorities.delete(key);
        },
      },
      getTotalLength() {
        this.measurements++;
        return length;
      },
      animate(frames, timing) {
        const animation = {
          frames,
          timing,
          onfinish: null,
          cancelled: false,
          cancel() {
            this.cancelled = true;
          },
        };
        animations.push(animation);
        return animation;
      },
    };
  });
  const controller = createStrokeMotion({ querySelectorAll: () => elements });
  return { controller, elements, animations };
}

// Minimal browser boundary: the controller still performs real scheduling,
// interruption and inline-style restoration against these geometry fixtures.
const originalComputedStyle = globalThis.getComputedStyle;
globalThis.getComputedStyle = (element) => ({
  strokeDashoffset:
    element.computed["stroke-dashoffset"] ||
    element.style.getPropertyValue("stroke-dashoffset") ||
    "0px",
  opacity:
    element.computed.opacity ||
    element.style.getPropertyValue("opacity") ||
    "1",
  getPropertyValue: (key) =>
    element.computed[key] ||
    element.style.getPropertyValue(key) ||
    { "stroke-dasharray": "none", "stroke-dashoffset": "0px", opacity: "1" }[
      key
    ],
});
test.after(() => {
  if (originalComputedStyle)
    globalThis.getComputedStyle = originalComputedStyle;
  else delete globalThis.getComputedStyle;
});

test("settled polls do no geometry work; entry staggers strokes and fades tiny dots", () => {
  const { controller, elements, animations } = fixture();
  controller.update(null, false);
  assert.equal(elements[0].measurements, 0);
  controller.update("enter", false);
  assert.equal(animations.length, 2);
  assert.equal(animations[0].frames[0].strokeDashoffset, 20);
  assert.deepEqual(animations[1].frames, [{ opacity: 0 }, { opacity: 1 }]);
  assert.equal(
    animations[1].timing.delay,
    iconMotion.enter.delay + iconMotion.enter.stagger,
  );
  controller.update("enter", false);
  assert.equal(animations.length, 2);
  animations.forEach((animation) => animation.onfinish());
  assert.ok(animations.every((animation) => animation.cancelled));
  controller.dispose();
});

test("interrupted strokes retract from the visible offset and preserve original inline styles", () => {
  const { controller, elements, animations } = fixture([20]);
  const stroke = elements[0];
  stroke.style.setProperty("stroke-dasharray", "2 3", "important");
  stroke.style.setProperty("opacity", "0.8");
  controller.update("enter", false);
  stroke.computed["stroke-dashoffset"] = "7px";
  const staleFinish = animations[0].onfinish;
  controller.interrupt();
  controller.update("leave", false);
  assert.equal(animations[1].frames[0].strokeDashoffset, 7);
  assert.equal(animations[1].frames[1].strokeDashoffset, 20);
  staleFinish();
  assert.equal(animations[1].cancelled, false);
  controller.dispose();
  assert.equal(stroke.style.getPropertyValue("stroke-dasharray"), "2 3");
  assert.equal(
    stroke.style.getPropertyPriority("stroke-dasharray"),
    "important",
  );
  assert.equal(stroke.style.getPropertyValue("stroke-dashoffset"), "");
  assert.equal(stroke.style.getPropertyValue("opacity"), "0.8");
  assert.ok(animations.every((animation) => animation.cancelled));
  controller.update("enter", false);
  assert.equal(animations.length, 2);
});

test("live reduced-motion changes restore native geometry without replay on re-enable", () => {
  const { controller, elements, animations } = fixture([20]);
  controller.update("enter", false);
  elements[0].computed["stroke-dashoffset"] = "9px";
  controller.interrupt();
  controller.update("enter", true);
  assert.equal(elements[0].style.getPropertyValue("stroke-dashoffset"), "");
  assert.equal(animations[0].cancelled, true);
  controller.update("enter", false);
  assert.equal(animations.length, 1);
  controller.update("leave", true);
  controller.update("leave", false);
  assert.equal(animations.length, 1);
  controller.dispose();
});

test("outgoing retention covers the longest stagger, and loading-only layers create no stroke animations", () => {
  assert.ok(iconExitRetention > iconMotion.leave.layer);
  assert.ok(
    iconExitRetention >
      iconMotion.leave.stroke +
        iconMotion.leave.delay +
        iconMotion.leave.stagger * iconMotion.leave.staggerLimit,
  );
  const { controller, animations } = fixture([]);
  controller.update("enter", false);
  controller.update("leave", false);
  assert.equal(animations.length, 0);
  controller.dispose();
});
