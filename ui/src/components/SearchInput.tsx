/** The search field of a list. The label is its placeholder and its accessible name. */
export function SearchInput(props: { value: string; onInput: (value: string) => void; label: string }) {
  return (
    <input
      type="search"
      class="input input-sm w-60 max-w-full"
      placeholder={props.label}
      aria-label={props.label}
      value={props.value}
      onInput={(e) => props.onInput(e.currentTarget.value)}
    />
  );
}
