import { useId } from 'react'

/*
The two controls that repeat, extracted because they repeat.

With utility classes the component *is* the name — there is no `.button--primary`
to look up, so a button that should look like every other button has to be the
same component rather than the same string copied. These two are the only ones
used in more than one place; anything used once is styled where it is used,
because naming it would be inventing a vocabulary nobody reads.
*/

const base =
  'cursor-pointer rounded-md border border-transparent font-medium transition-colors ' +
  'disabled:cursor-default disabled:opacity-60'

const tones = {
  primary: 'bg-accent text-on-accent enabled:hover:bg-accent-strong',
  plain: 'bg-surface-raised enabled:hover:bg-surface-hover',
} as const

const sizes = {
  regular: 'px-4 py-2.5',
  small: 'px-3 py-1 text-[0.78rem]',
} as const

interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  tone?: keyof typeof tones
  size?: keyof typeof sizes
}

export function Button({
  tone = 'plain',
  size = 'regular',
  className = '',
  type = 'button',
  ...rest
}: ButtonProps) {
  return (
    <button
      // eslint-disable-next-line react/button-has-type -- defaulted above.
      type={type}
      className={`${base} ${tones[tone]} ${sizes[size]} ${className}`}
      {...rest}
    />
  )
}

/*
input is the class list a text field wears, exported rather than wrapped.

An input takes too many different props to hide behind a component, and the one
thing that has to be identical across them is how it looks — so that is what is
shared.
*/
export const input =
  'rounded-md border border-line bg-surface-raised px-3 py-2.5 transition-colors ' +
  'hover:border-line-strong focus:border-accent focus:ring-3 focus:ring-accent-soft focus:outline-none'

interface FieldProps extends React.InputHTMLAttributes<HTMLInputElement> {
  label: string
}

// Field is a labelled text input. The label is bound by id rather than by
// wrapping, so a screen reader announces it for the input and nothing else.
export function Field({ label, ...rest }: FieldProps) {
  const id = useId()

  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className="text-[0.8rem] font-medium text-ink-dim">
        {label}
      </label>
      <input id={id} className={input} {...rest} />
    </div>
  )
}
