import {
  useId,
  useState,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
  type TextareaHTMLAttributes,
} from 'react'
import { IconButton } from './Button'

function describedByOf(fieldId: string, error?: string, hint?: string): string | undefined {
  const ids = [error ? `${fieldId}-error` : null, hint ? `${fieldId}-hint` : null].filter(
    (v): v is string => v !== null,
  )
  return ids.length > 0 ? ids.join(' ') : undefined
}

interface ShellProps {
  fieldId: string
  label: string
  error?: string | undefined
  hint?: string | undefined
  required?: boolean | undefined
  control: ReactNode
}

function Shell({ fieldId, label, error, hint, required, control }: ShellProps) {
  return (
    <div className={`field${error ? ' field-invalid' : ''}`}>
      <label className={`field-label${required ? ' field-label-required' : ''}`} htmlFor={fieldId}>
        {label}
      </label>
      {control}
      {hint ? (
        <p className="field-hint" id={`${fieldId}-hint`}>
          {hint}
        </p>
      ) : null}
      {error ? (
        <p className="field-error-text" id={`${fieldId}-error`} role="alert">
          {error}
        </p>
      ) : null}
    </div>
  )
}

export interface TextFieldProps extends InputHTMLAttributes<HTMLInputElement> {
  label: string
  error?: string | undefined
  hint?: string | undefined
}

export function TextField({ label, error, hint, id, ...rest }: TextFieldProps) {
  const autoId = useId()
  const fieldId = id ?? autoId
  return (
    <Shell
      fieldId={fieldId}
      label={label}
      error={error}
      hint={hint}
      required={rest.required}
      control={
        <input
          id={fieldId}
          className="input"
          aria-invalid={error ? true : undefined}
          aria-describedby={describedByOf(fieldId, error, hint)}
          {...rest}
        />
      }
    />
  )
}

export interface PasswordFieldProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'type'> {
  label: string
  error?: string | undefined
  hint?: string | undefined
}

/** 密码/密钥输入：带「显示/隐藏」切换，切换按钮不进入 Tab 序后的表单提交路径（type=button）。 */
export function PasswordField({ label, error, hint, id, ...rest }: PasswordFieldProps) {
  const autoId = useId()
  const fieldId = id ?? autoId
  const [visible, setVisible] = useState(false)
  return (
    <Shell
      fieldId={fieldId}
      label={label}
      error={error}
      hint={hint}
      required={rest.required}
      control={
        <div className="input-group">
          <input
            id={fieldId}
            className="input"
            type={visible ? 'text' : 'password'}
            aria-invalid={error ? true : undefined}
            aria-describedby={describedByOf(fieldId, error, hint)}
            {...rest}
          />
          <IconButton
            className="input-group-btn"
            icon={visible ? 'eye-off' : 'eye'}
            label={visible ? '隐藏内容' : '显示内容'}
            aria-pressed={visible}
            onClick={() => setVisible((v) => !v)}
          />
        </div>
      }
    />
  )
}

export interface TextAreaFieldProps extends TextareaHTMLAttributes<HTMLTextAreaElement> {
  label: string
  error?: string | undefined
  hint?: string | undefined
}

export function TextAreaField({ label, error, hint, id, className, ...rest }: TextAreaFieldProps) {
  const autoId = useId()
  const fieldId = id ?? autoId
  return (
    <Shell
      fieldId={fieldId}
      label={label}
      error={error}
      hint={hint}
      required={rest.required}
      control={
        <textarea
          id={fieldId}
          className={className ? `input ${className}` : 'input'}
          aria-invalid={error ? true : undefined}
          aria-describedby={describedByOf(fieldId, error, hint)}
          {...rest}
        />
      }
    />
  )
}

export interface SelectFieldProps extends SelectHTMLAttributes<HTMLSelectElement> {
  label: string
  error?: string | undefined
  hint?: string | undefined
  children: ReactNode
}

export function SelectField({ label, error, hint, id, children, ...rest }: SelectFieldProps) {
  const autoId = useId()
  const fieldId = id ?? autoId
  return (
    <Shell
      fieldId={fieldId}
      label={label}
      error={error}
      hint={hint}
      required={rest.required}
      control={
        <select
          id={fieldId}
          className="input"
          aria-invalid={error ? true : undefined}
          aria-describedby={describedByOf(fieldId, error, hint)}
          {...rest}
        >
          {children}
        </select>
      }
    />
  )
}
