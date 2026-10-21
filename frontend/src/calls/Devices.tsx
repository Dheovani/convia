import { useEffect, useId, useState } from "react";
import { input } from "../components/controls";
import { useWords } from "../i18n/language";
import {
  rememberDevice,
  rememberedDevice,
  type Device,
} from "../state/preferences";
import { devices } from "./media";

const kinds: Record<Device, MediaDeviceKind> = {
  microphone: "audioinput",
  camera: "videoinput",
  speaker: "audiooutput",
};

// Devices is where a person chooses the microphone, camera and speaker their calls use.
export function Devices() {
  const words = useWords();
  const id = useId();
  const [found, setFound] = useState<MediaDeviceInfo[]>([]);
  const [chosen, setChosen] = useState<Record<Device, string>>(() => ({
    microphone: rememberedDevice("microphone"),
    camera: rememberedDevice("camera"),
    speaker: rememberedDevice("speaker"),
  }));

  useEffect(() => {
    void devices().then(setFound, () => undefined);
  }, []);

  return (
    <div className="flex w-full max-w-lg flex-col gap-4">
      <h3 className="font-medium">{words.calls.settings}</h3>
      {(Object.keys(kinds) as Device[]).map((kind) => (
        <div key={kind} className="flex flex-col gap-1">
          <label
            htmlFor={`${id}-${kind}`}
            className="text-[0.8rem] font-medium text-ink-dim"
          >
            {words.calls[kind]}
          </label>
          <select
            id={`${id}-${kind}`}
            className={input}
            value={chosen[kind]}
            onChange={(event) => {
              const next = event.target.value;
              setChosen({ ...chosen, [kind]: next });
              rememberDevice(kind, next);
            }}
          >
            <option value="">{words.calls.systemDevice}</option>
            {found
              .filter((d) => d.kind === kinds[kind] && d.deviceId !== "default")
              .map((d, i) => (
                <option key={d.deviceId || i} value={d.deviceId}>
                  {d.label || `${words.calls[kind]} ${i + 1}`}
                </option>
              ))}
          </select>
        </div>
      ))}
      <p className="text-xs text-ink-faint">{words.calls.devicesHint}</p>
    </div>
  );
}
