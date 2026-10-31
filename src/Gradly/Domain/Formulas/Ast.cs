namespace Gradly.Domain.Formulas;

internal abstract record Node(int Position);
internal sealed record NumberNode(double Value, int Position) : Node(Position);
internal sealed record VariableNode(string Name, int Position) : Node(Position);
internal sealed record UnaryNode(char Op, Node Operand, int Position) : Node(Position);
internal sealed record BinaryNode(char Op, Node Left, Node Right, int Position) : Node(Position);
internal sealed record CallNode(string Name, IReadOnlyList<Node> Args, int Position) : Node(Position);
