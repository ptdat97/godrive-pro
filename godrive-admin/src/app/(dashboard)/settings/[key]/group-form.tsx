"use client";

import { useActionState, useMemo, useState } from "react";
import { useFormStatus } from "react-dom";
import { saveSettings, type SaveState } from "./actions";
import type { SettingField, SettingGroup } from "@/lib/types";

// ---------------------------------------------------------------- đường dẫn

type Json = Record<string, unknown>;

function getPath(obj: Json, path: string): unknown {
  return path.split(".").reduce<unknown>((cur, part) => {
    if (cur === null || typeof cur !== "object") return undefined;
    return (cur as Json)[part];
  }, obj);
}

/** Trả bản sao mới với một ô đã đổi. Không sửa tại chỗ, để React thấy thay đổi. */
function setPath(obj: Json, path: string, val: unknown): Json {
  const parts = path.split(".");
  const out: Json = { ...obj };
  let cur: Json = out;
  for (const part of parts.slice(0, -1)) {
    cur[part] = { ...((cur[part] ?? {}) as Json) };
    cur = cur[part] as Json;
  }
  cur[parts[parts.length - 1]] = val;
  return out;
}

// ---------------------------------------------------------------- định dạng

const nf = new Intl.NumberFormat("vi-VN");

/** Dòng quy đổi dưới ô nhập: con số vừa gõ có nghĩa là gì. */
function unitHint(kind: string, n: number): string {
  if (!Number.isFinite(n)) return "";
  switch (kind) {
    case "vnd":
      return `${nf.format(n)}₫`;
    case "permille":
      return `${nf.format(n / 10)}% — nhân ${nf.format(n / 1000)} lần`;
    case "seconds":
      if (n < 60) return `${n} giây`;
      return n % 60 === 0
        ? `${n / 60} phút`
        : `${Math.floor(n / 60)} phút ${n % 60} giây`;
    case "meters":
      return n >= 1000 ? `${nf.format(n / 1000)} km` : `${nf.format(n)} m`;
    case "hour":
      return `${String(n).padStart(2, "0")}:00 giờ Việt Nam`;
    default:
      return "";
  }
}

/** Lỗi ngưỡng, hoặc null nếu hợp lệ. Chỉ để báo sớm — máy chủ vẫn kiểm lại. */
function rangeError(f: SettingField, raw: unknown): string | null {
  if (f.kind === "bool" || f.kind === "surge_steps") return null;
  const n = Number(raw);
  if (raw === "" || raw === null || !Number.isFinite(n)) return "Chưa nhập số.";
  if (f.min !== undefined && n < f.min)
    return `Phải từ ${nf.format(f.min)} trở lên.`;
  if (f.max !== undefined && n > f.max)
    return `Không được quá ${nf.format(f.max)}.`;
  if (f.kind !== "float" && !Number.isInteger(n)) return "Phải là số nguyên.";
  return null;
}

// ---------------------------------------------------------------- bậc thang

interface Step {
  ratio_x10: number;
  permille: number;
}

function StepsEditor({
  steps,
  onChange,
}: {
  steps: Step[];
  onChange: (next: Step[]) => void;
}) {
  const edit = (i: number, patch: Partial<Step>) =>
    onChange(steps.map((s, j) => (j === i ? { ...s, ...patch } : s)));

  return (
    <div className="space-y-2">
      <div className="overflow-x-auto">
        <table className="w-full min-w-[34rem] text-sm">
          <thead>
            <tr className="text-left">
              <th className="pb-1 text-xs font-semibold text-zinc-500 uppercase">
                Khi cầu/cung đạt
              </th>
              <th className="pb-1 text-xs font-semibold text-zinc-500 uppercase">
                Thì nhân giá
              </th>
              <th />
            </tr>
          </thead>
          <tbody>
            {steps.map((s, i) => {
              const prev = steps[i - 1];
              const badRatio = prev && s.ratio_x10 <= prev.ratio_x10;
              const badPermille = prev && s.permille <= prev.permille;
              return (
                <tr key={i}>
                  <td className="py-1 pr-3">
                    <div className="flex items-center gap-2">
                      <NumberBox
                        value={s.ratio_x10}
                        invalid={!!badRatio}
                        onChange={(v) => edit(i, { ratio_x10: v })}
                      />
                      <span className="text-xs whitespace-nowrap text-zinc-500">
                        = {nf.format(s.ratio_x10 / 10)} lần
                      </span>
                    </div>
                  </td>
                  <td className="py-1 pr-3">
                    <div className="flex items-center gap-2">
                      <NumberBox
                        value={s.permille}
                        invalid={!!badPermille}
                        onChange={(v) => edit(i, { permille: v })}
                      />
                      <span className="text-xs whitespace-nowrap text-zinc-500">
                        = ×{nf.format(s.permille / 1000)}
                      </span>
                    </div>
                  </td>
                  <td className="py-1">
                    <button
                      type="button"
                      onClick={() => onChange(steps.filter((_, j) => j !== i))}
                      className="rounded-md px-2 py-1 text-xs text-zinc-500 transition hover:bg-red-50 hover:text-red-600"
                    >
                      Xoá
                    </button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {steps.length === 0 && (
        <p className="text-xs text-amber-700">
          Không có bậc nào — hệ số sẽ luôn là 1,0 dù đang bật tăng giá.
        </p>
      )}
      {steps.length < 10 && (
        <button
          type="button"
          onClick={() => {
            const last = steps[steps.length - 1];
            onChange([
              ...steps,
              {
                ratio_x10: last ? last.ratio_x10 + 10 : 12,
                permille: last ? last.permille + 100 : 1200,
              },
            ]);
          }}
          className="rounded-lg border border-zinc-300 px-3 py-1.5 text-xs font-medium text-zinc-700 transition hover:bg-zinc-50"
        >
          Thêm bậc
        </button>
      )}
      <p className="text-xs text-zinc-500">
        Ngưỡng và hệ số đều phải tăng dần theo từng dòng.
      </p>
    </div>
  );
}

function NumberBox({
  value,
  onChange,
  invalid,
}: {
  value: number;
  onChange: (v: number) => void;
  invalid?: boolean;
}) {
  return (
    <input
      type="number"
      value={value}
      onChange={(e) => onChange(Number(e.target.value))}
      className={`w-28 rounded-lg border px-2 py-1 text-sm tabular-nums outline-none focus:ring-2 ${
        invalid
          ? "border-red-300 bg-red-50 focus:ring-red-200"
          : "border-zinc-300 focus:ring-zinc-200"
      }`}
    />
  );
}

// ---------------------------------------------------------------- biểu mẫu

export default function GroupForm({ group }: { group: SettingGroup }) {
  const [state, submit] = useActionState<SaveState, FormData>(saveSettings, {});

  // Bản của máy chủ là mốc duy nhất. Chuỗi hoá để so sánh được bằng ===, vì
  // mỗi lần render lại thì group.value là một object khác dù nội dung y hệt.
  const serverSnapshot = JSON.stringify(group.value);

  const [value, setValue] = useState<Json>(() =>
    structuredClone(group.value as Json),
  );
  const [reason, setReason] = useState("");
  const [seen, setSeen] = useState(serverSnapshot);

  // Máy chủ vừa trả dữ liệu khác (ta lưu xong, hoặc người khác vừa sửa): lấy
  // làm mốc mới ngay trong lúc render.
  //
  // Không dùng useEffect: effect chạy SAU khi màn hình đã vẽ, nên người dùng sẽ
  // thấy một khung hình với giá trị cũ trước khi nó nhảy sang giá trị mới.
  if (seen !== serverSnapshot) {
    setSeen(serverSnapshot);
    setValue(structuredClone(group.value as Json));
    setReason("");
  }

  const allFields = useMemo(
    () => group.sections.flatMap((s) => s.fields),
    [group.sections],
  );

  const errors = useMemo(() => {
    const out: Record<string, string> = {};
    for (const f of allFields) {
      const err = rangeError(f, getPath(value, f.path));
      if (err) out[f.path] = err;
    }
    return out;
  }, [allFields, value]);

  const dirty = JSON.stringify(value) !== serverSnapshot;
  const blocked = Object.keys(errors).length > 0;
  const conflict = state.code === "setting_version_conflict";

  return (
    <form action={submit} className="space-y-5">
      <input type="hidden" name="key" value={group.key} />
      <input type="hidden" name="version" value={group.version} />
      <input type="hidden" name="value" value={JSON.stringify(value)} />

      {group.warning && (
        <div className="rounded-xl border border-amber-200 bg-amber-50 px-4 py-3">
          <p className="text-xs font-semibold tracking-wide text-amber-800 uppercase">
            Cần biết trước khi đổi
          </p>
          <p className="mt-1 text-sm text-amber-900">{group.warning}</p>
        </div>
      )}

      {group.sections.map((sec) => (
        <section
          key={sec.title}
          className="rounded-xl border border-zinc-200 bg-white shadow-sm"
        >
          <header className="border-b border-zinc-100 px-5 py-3">
            <h3 className="text-sm font-semibold text-zinc-900">{sec.title}</h3>
            {sec.note && (
              <p className="mt-1 text-xs leading-relaxed text-zinc-500">
                {sec.note}
              </p>
            )}
          </header>
          <div className="grid gap-x-8 gap-y-4 px-5 py-4 sm:grid-cols-2">
            {sec.fields.map((f) => (
              <FieldRow
                key={f.path}
                field={f}
                raw={getPath(value, f.path)}
                error={errors[f.path]}
                onChange={(v) => setValue((cur) => setPath(cur, f.path, v))}
              />
            ))}
          </div>
        </section>
      ))}

      <div className="rounded-xl border border-zinc-200 bg-white px-5 py-4 shadow-sm">
        <label
          htmlFor="reason"
          className="text-sm font-medium text-zinc-900"
        >
          Lý do thay đổi
        </label>
        <p className="mt-0.5 text-xs text-zinc-500">
          Ghi vào lịch sử và nhật ký thao tác. Người đọc lại sau này chỉ còn dòng
          này để hiểu vì sao con số là như vậy.
        </p>
        <input
          id="reason"
          name="reason"
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder="Ví dụ: điều chỉnh theo giá xăng tháng 8, đã nộp hồ sơ kê khai ngày 20/8"
          className="mt-2 w-full rounded-lg border border-zinc-300 px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-zinc-200"
        />

        <div className="mt-4 flex flex-wrap items-center gap-3">
          <SubmitButton disabled={!dirty || blocked || reason.trim().length < 5} />
          {dirty && (
            <button
              type="button"
              onClick={() => {
                setValue(structuredClone(group.value as Json));
                setReason("");
              }}
              className="rounded-lg border border-zinc-300 px-3 py-2 text-sm font-medium text-zinc-700 transition hover:bg-zinc-50"
            >
              Bỏ thay đổi
            </button>
          )}
          <span className="text-xs text-zinc-500">
            {blocked
              ? `${Object.keys(errors).length} ô chưa hợp lệ.`
              : dirty
                ? "Có thay đổi chưa lưu."
                : `Phiên bản ${group.version}${group.is_default ? " — đang dùng mặc định trong code" : ""}`}
          </span>
        </div>

        {state.error && (
          <div className="mt-3 rounded-lg border border-red-200 bg-red-50 px-3 py-2">
            <p className="text-sm text-red-700">{state.error}</p>
            {conflict && (
              <p className="mt-1 text-xs text-red-600">
                Có người khác vừa sửa nhóm này. Tải lại trang để xem giá trị mới
                rồi nhập lại thay đổi của bạn — hệ thống không tự gộp hai bản sửa.
              </p>
            )}
            {state.code && (
              <p className="mt-1 font-mono text-xs text-red-400">{state.code}</p>
            )}
          </div>
        )}
        {state.savedVersion !== undefined && !dirty && !state.error && (
          <p className="mt-3 text-sm text-emerald-600">
            Đã lưu. Thay đổi có hiệu lực trong vòng vài giây, áp dụng cho báo giá
            và chuyến phát sinh từ giờ trở đi.
          </p>
        )}
      </div>
    </form>
  );
}

function FieldRow({
  field,
  raw,
  error,
  onChange,
}: {
  field: SettingField;
  raw: unknown;
  error?: string;
  onChange: (v: unknown) => void;
}) {
  if (field.kind === "bool") {
    return (
      <label className="flex items-start gap-3 sm:col-span-2">
        <input
          type="checkbox"
          checked={Boolean(raw)}
          onChange={(e) => onChange(e.target.checked)}
          className="mt-0.5 h-4 w-4 rounded border-zinc-300"
        />
        <span>
          <span className="text-sm font-medium text-zinc-900">
            {field.label}
          </span>
          {field.hint && (
            <span className="block text-xs text-zinc-500">{field.hint}</span>
          )}
        </span>
      </label>
    );
  }

  if (field.kind === "surge_steps") {
    return (
      <div className="sm:col-span-2">
        <StepsEditor
          steps={(raw as Step[]) ?? []}
          onChange={(next) => onChange(next)}
        />
      </div>
    );
  }

  const n = Number(raw);
  const hint = unitHint(field.kind, n);
  return (
    <div>
      <label className="block text-sm font-medium text-zinc-900">
        {field.label}
      </label>
      <input
        type="number"
        value={raw === undefined || raw === null ? "" : String(raw)}
        step={field.kind === "float" ? "any" : 1}
        min={field.min}
        max={field.max}
        onChange={(e) =>
          onChange(e.target.value === "" ? "" : Number(e.target.value))
        }
        className={`mt-1 w-full rounded-lg border px-3 py-2 text-sm tabular-nums outline-none focus:ring-2 ${
          error
            ? "border-red-300 bg-red-50 focus:ring-red-200"
            : "border-zinc-300 focus:ring-zinc-200"
        }`}
      />
      <p className="mt-1 text-xs">
        {error ? (
          <span className="text-red-600">{error}</span>
        ) : (
          <span className="text-zinc-500">
            {hint}
            {hint && field.hint ? " · " : ""}
            {field.hint}
          </span>
        )}
      </p>
    </div>
  );
}

function SubmitButton({ disabled }: { disabled: boolean }) {
  const { pending } = useFormStatus();
  return (
    <button
      type="submit"
      disabled={disabled || pending}
      className="rounded-lg bg-zinc-900 px-4 py-2 text-sm font-medium text-white transition hover:bg-zinc-800 disabled:cursor-not-allowed disabled:opacity-40"
    >
      {pending ? "Đang lưu…" : "Lưu thay đổi"}
    </button>
  );
}
