/** 注入值要原样进 HTTP 头，长度与字符集与服务端的 validCodexTurnState 保持一致。 */
export const credentialTurnStateMaxLength = 4096

export function validCredentialTurnState(value: string): boolean {
  return (
    value.length <= credentialTurnStateMaxLength &&
    ![...value].some((char) => char < ' ' || char > '~')
  )
}
