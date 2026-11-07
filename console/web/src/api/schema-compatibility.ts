import type { components } from './generated/management'
import type {
  App,
  Client,
  User,
  Operator,
  UserSession,
  AuditEvent,
  Connection,
  AuthenticationConfig,
} from './types'

// A generated server response must remain assignable to the UI's display DTO.
// This catches field/type drift while allowing the UI to render future status strings safely.
type Assert<T extends true> = T
type Extends<A, B> = A extends B ? true : false
export type ContractCompatibility = [
  Assert<Extends<components['schemas']['App'], App>>,
  Assert<Extends<components['schemas']['Client'], Client>>,
  Assert<Extends<components['schemas']['User'], User>>,
  Assert<Extends<components['schemas']['Operator'], Operator>>,
  Assert<Extends<components['schemas']['Session'], UserSession>>,
  Assert<Extends<components['schemas']['Audit'], AuditEvent>>,
  Assert<Extends<components['schemas']['Connection'], Connection>>,
  Assert<Extends<components['schemas']['AuthenticationConfig'], AuthenticationConfig>>,
]
