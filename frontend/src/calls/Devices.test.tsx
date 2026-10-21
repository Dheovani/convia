import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, expect, test } from "vitest";
import { rememberedDevice } from "../state/preferences";
import { Devices } from "./Devices";

beforeEach(() => {
  localStorage.clear();
  Object.defineProperty(navigator, "mediaDevices", {
    configurable: true,
    value: {
      enumerateDevices: () =>
        Promise.resolve([
          { kind: "audioinput", deviceId: "default", label: "Default" },
          { kind: "audioinput", deviceId: "m1", label: "Headset" },
          { kind: "videoinput", deviceId: "v1", label: "" },
          { kind: "audiooutput", deviceId: "s1", label: "Speakers" },
        ]),
    },
  });
});

test("a person chooses the devices their calls use, and the choice is remembered", async () => {
  render(<Devices />);
  expect(await screen.findByRole("option", { name: "Headset" })).toBeTruthy();
  // A device nameless until a call used it is still offered, by its kind.
  expect(screen.getByRole("option", { name: "Camera 1" })).toBeTruthy();
  expect(screen.queryByRole("option", { name: "Default" })).toBeNull();
  fireEvent.change(screen.getByLabelText("Microphone"), {
    target: { value: "m1" },
  });
  fireEvent.change(screen.getByLabelText("Speaker"), {
    target: { value: "s1" },
  });
  expect(rememberedDevice("microphone")).toBe("m1");
  expect(rememberedDevice("speaker")).toBe("s1");
  expect(rememberedDevice("camera")).toBe("");
});
