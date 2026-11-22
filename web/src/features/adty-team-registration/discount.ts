// Four fractional digits match the stored agreement price.
export function validTeamDiscount(input: string): boolean {
  return (
    /^(?:0?\.\d{1,4}0*|1(?:\.0+)?)$/.test(input.trim()) && Number(input) > 0
  )
}
